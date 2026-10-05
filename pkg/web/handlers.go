package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/memory"
)

// maxLoginBody 限制登录请求体大小（口令远小于此，防超大 body 打内存）。
const maxLoginBody = 4 << 10

// searchLimitMax 是检索返回条数上限。
const searchLimitMax = 100

// 路径字面形态上限：超限属于客户端输入错误（应 4xx），不是服务端故障。
const (
	maxRelPathLen       = 4096 // 整串上限
	maxPathComponentLen = 255  // 单级分量上限（对齐常见文件系统）
)

// handleLoginPage 输出公开登录页（纯静态，不含任何库内容）。
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.serveStatic(w, "login.html")
}

// handleLogin 校验口令并下发持久 Cookie。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.auth.Allowed(ip) {
		s.writeJSONError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLoginBody)).Decode(&body); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !s.auth.Valid(strings.TrimSpace(body.Token)) {
		s.auth.Fail(ip)
		// 不回显任何输入内容与差异信息。
		s.writeJSONError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	s.auth.Succeed(ip)
	SetAuthCookie(w, r, s.auth.token)
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLogout 清除登录 Cookie。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ClearAuthCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// handleIndexPage 输出阅读页外壳（仅 /memory/ 一个路径）。
func (s *Server) handleIndexPage(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; p != "/memory/" && p != "/memory" {
		s.writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	s.serveStatic(w, "index.html")
}

// statusResponse 是 /memory/api/status 的响应体。
type statusResponse struct {
	Files        int    `json:"files"`
	Bytes        int64  `json:"bytes"`
	LastModified string `json:"last_modified,omitempty"`
}

// handleStatus 返回库统计。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Status()
	if err != nil {
		s.storeError(w, err)
		return
	}
	resp := statusResponse{Files: st.FileCount, Bytes: st.TotalBytes}
	if !st.LastModified.IsZero() {
		resp.LastModified = st.LastModified.Format(time.RFC3339)
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// treeEntry 是目录项。
type treeEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"dir"`
}

// treeResponse 是 /memory/api/tree 的响应体。
type treeResponse struct {
	Path    string      `json:"path"`
	Entries []treeEntry `json:"entries"`
}

// handleTree 列出目录（不递归）。
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	entries, err := s.store.List(dir)
	if err != nil {
		s.storeError(w, err)
		return
	}
	resp := treeResponse{Path: dir, Entries: make([]treeEntry, 0, len(entries))}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, treeEntry{Name: e.Name, IsDir: e.IsDir})
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// fileResponse 是 /memory/api/file 的响应体。
// kind=markdown 时给 html；kind=text 时给 text（前端用 textContent 写入，
// 因此这里给**原文**而不是预转义文本，避免二次转义）；
// kind=binary/large 时只给大小与 mtime，内容走下载接口。
type fileResponse struct {
	Path  string `json:"path"`
	Size  int    `json:"size"`
	MTime string `json:"mtime,omitempty"`
	Kind  string `json:"kind"`
	HTML  string `json:"html,omitempty"`
	Text  string `json:"text,omitempty"`
}

// handleFile 读取单个文件并按类型给出展示内容。
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if err := validateRelPath(rel); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.store.Read(rel)
	if err != nil {
		if s.pathIsDir(rel) {
			// 目录：Linux 上 ReadFile 返回 EISDIR、Windows 上是访问被拒——
			// 统一按客户端错误 400，不污染 5xx。
			s.writeJSONError(w, http.StatusBadRequest, "path is a directory (use the tree API)")
			return
		}
		s.storeError(w, err)
		return
	}
	resp := fileResponse{Path: rel, Size: len(data), MTime: s.mtimeOf(rel)}

	if len(data) > maxRenderBytes {
		resp.Kind = string(KindLarge)
		s.writeJSON(w, http.StatusOK, resp)
		return
	}
	switch kind := KindOf(rel, data); kind {
	case KindBinary:
		resp.Kind = string(KindBinary)
	case KindMarkdown:
		rendered, err := RenderMarkdown(data)
		if err != nil {
			s.storeError(w, err)
			return
		}
		resp.Kind = string(KindMarkdown)
		resp.HTML = rendered
	default:
		resp.Kind = string(KindText)
		resp.Text = string(data)
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// handleRaw 原样输出文件字节，供下载（内容与库中文件逐字节一致）。
func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if err := validateRelPath(rel); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.store.Read(rel)
	if err != nil {
		if s.pathIsDir(rel) {
			s.writeJSONError(w, http.StatusBadRequest, "path is a directory (use the tree API)")
			return
		}
		s.storeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	// RFC 5987 编码文件名：库内中文文件名也能正确落到下载目录。
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(path.Base(filepath.ToSlash(rel))))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		s.logf("web: write raw %s: %v", sanitizeLogField(rel), err)
	}
}

// validateRelPath 校验路径的字面形态：空、超长、单级分量超长、含控制字符
// （含 NUL）一律判为客户端输入错误。**不做文件系统访问**——沙箱判定交给
// store.Read，避免在越界路径上产生"根外是否存在"的探测口。
func validateRelPath(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return errors.New("missing path")
	}
	if len(rel) > maxRelPathLen {
		return fmt.Errorf("path too long (max %d bytes)", maxRelPathLen)
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if len(part) > maxPathComponentLen {
			return fmt.Errorf("path component too long (max %d bytes)", maxPathComponentLen)
		}
	}
	if strings.ContainsFunc(rel, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return errors.New("invalid path (control character)")
	}
	return nil
}

// pathIsDir 报告 rel 是否为目录。**只在 store.Read 失败后调用**：此时 rel 已
// 通过沙箱校验（越界路径不会走到这里），Stat 不构成越界信息泄露；Stat 自身
// 失败一律按"不是目录"处理，交由 storeError 归类。
func (s *Server) pathIsDir(rel string) bool {
	info, err := os.Stat(filepath.Join(s.store.Root(), filepath.FromSlash(rel)))
	return err == nil && info.IsDir()
}

// searchHit 是单条命中。
type searchHit struct {
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

// searchResponse 是 /memory/api/search 的响应体。
type searchResponse struct {
	Mode  string      `json:"mode"`
	Count int         `json:"count"`
	Hits  []searchHit `json:"hits"`
}

// handleSearch 全文检索：q 为关键词（空格分隔多词），mode=and|or（默认 and）。
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	mode, err := index.ParseMode(r.URL.Query().Get("mode"))
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			s.writeJSONError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > searchLimitMax {
			n = searchLimitMax
		}
		limit = n
	}

	results, err := s.ix.SearchMode(q, limit, mode)
	if err != nil {
		s.storeError(w, err)
		return
	}
	resp := searchResponse{Mode: mode.String(), Count: len(results), Hits: make([]searchHit, 0, len(results))}
	for _, hit := range results {
		resp.Hits = append(resp.Hits, searchHit{Path: hit.Path, Snippet: hit.Snippet})
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// mtimeOf 返回文件修改时间（RFC3339）；取不到时返回空串（展示层面可缺省）。
// 只读元数据：rel 已在 store.Read 中通过路径沙箱校验，此处仅做拼接与 stat。
func (s *Server) mtimeOf(rel string) string {
	info, err := os.Stat(filepath.Join(s.store.Root(), filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return info.ModTime().Format(time.RFC3339)
}

// storeError 把底层错误映射为对外状态码：路径类错误 400、不存在 404、
// 空查询 400，其余 500 且**只记日志不回显细节**。
func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, memory.ErrPathOutsideRoot):
		s.writeJSONError(w, http.StatusBadRequest, "invalid path")
	case errors.Is(err, os.ErrNotExist):
		s.writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, index.ErrEmptyQuery):
		s.writeJSONError(w, http.StatusBadRequest, "empty query")
	default:
		s.logf("web: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
