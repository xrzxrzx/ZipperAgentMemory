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

// handleTree 列出目录（不递归）。**点目录/点文件不列出**（见 hasHiddenComponent）。
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if hasHiddenComponent(dir) {
		s.writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	entries, err := s.store.List(dir)
	if err != nil {
		s.storeError(w, err)
		return
	}
	resp := treeResponse{Path: dir, Entries: make([]treeEntry, 0, len(entries))}
	for _, e := range entries {
		if strings.HasPrefix(e.Name, ".") {
			continue // 隐藏项不暴露（含 .git/ 等仓库内部）
		}
		resp.Entries = append(resp.Entries, treeEntry{Name: e.Name, IsDir: e.IsDir})
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// hasHiddenComponent 报告 rel 是否含以 "." 开头的路径分量（"." 与 ".." 除外，
// 前者表示根目录、后者由路径沙箱负责拒绝）。
//
// 用途：阅读页对点目录/点文件一律不可见（浏览、读取、下载都拒绝），避免把
// `.git/` 之类仓库内部内容暴露出去；检索侧天然不受影响——索引本就不收录
// 隐藏文件（pkg/index ShouldIndex）。
func hasHiddenComponent(rel string) bool {
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "." || part == ".." {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// fileResponse 是 /memory/api/file 的响应体。
// kind=markdown/csv 时给 html（服务端已逐单元格/逐段转义）；
// kind=text 时给 text（前端用 textContent 写入，因此这里给**原文**而不是
// 预转义文本，避免二次转义）；kind=binary/large 时只给大小与 mtime，
// 内容走下载接口。truncated 仅对表格有意义：行数超过渲染上限。
type fileResponse struct {
	Path      string `json:"path"`
	Size      int    `json:"size"`
	MTime     string `json:"mtime,omitempty"`
	Kind      string `json:"kind"`
	HTML      string `json:"html,omitempty"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// handleFile 读取单个文件并按类型给出展示内容。
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if hasHiddenComponent(rel) {
		s.writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
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
	case KindCSV:
		rendered, truncated, err := RenderCSV(data, csvDelimiter(rel), csvMaxRows)
		if err != nil {
			// 解析失败回退为纯文本展示：畸形 CSV 是内容问题，不该变成 500。
			s.logf("web: csv fallback to text for %s: %v", sanitizeLogField(rel), err)
			resp.Kind = string(KindText)
			resp.Text = string(data)
			break
		}
		resp.Kind = string(KindCSV)
		resp.HTML = rendered
		resp.Truncated = truncated
	default:
		resp.Kind = string(KindText)
		resp.Text = string(data)
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// handleRaw 原样输出文件字节，供下载（内容与库中文件逐字节一致）。
func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if hasHiddenComponent(rel) {
		s.writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
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

// pathIsDir 报告 rel 是否为目录。**元数据查询一律经 store 沙箱**（需求-004）：
// 越界路径在沙箱处即失败（ErrPathOutsideRoot）→ 按"不是目录"处理，交由
// storeError 归类。绝不在此自行拼接路径 + os.Stat——拼接会先把 `..` 归一化，
// 使根外目标被真正 Stat，从而暴露"根外是否存在目录"。
func (s *Server) pathIsDir(rel string) bool {
	isDir, err := s.store.IsDir(rel)
	return err == nil && isDir
}

// searchHit 是单条命中。
type searchHit struct {
	Path    string  `json:"path"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"` // 相关性分数：越大越相关；仅同一查询内可比
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
		resp.Hits = append(resp.Hits, searchHit{Path: hit.Path, Snippet: hit.Snippet, Score: hit.Score})
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// mtimeOf 返回文件修改时间（RFC3339）；取不到时返回空串（展示层面可缺省）。
// 元数据经 store.ModTime 沙箱完成（需求-004）：阅读页不直接接触文件系统。
func (s *Server) mtimeOf(rel string) string {
	mt, err := s.store.ModTime(rel)
	if err != nil {
		return ""
	}
	return mt.Format(time.RFC3339)
}

// storeError 把底层错误映射为对外状态码：路径类错误 400、不存在 404、
// 空查询 400，其余 500 且**只记日志不回显细节**。
func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, memory.ErrPathOutsideRoot):
		s.writeJSONError(w, http.StatusBadRequest, "invalid path")
	case errors.Is(err, memory.ErrTrashTargetExists):
		s.writeJSONError(w, http.StatusConflict, "restore target already exists")
	case errors.Is(err, memory.ErrTrashEntryNotFound):
		s.writeJSONError(w, http.StatusNotFound, "trash entry not found")
	case errors.Is(err, os.ErrNotExist):
		s.writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, index.ErrEmptyQuery):
		s.writeJSONError(w, http.StatusBadRequest, "empty query")
	case errors.Is(err, index.ErrIndexStale):
		// 需求-005 的索引版本保护：明确提示而不是笼统 500（P3-d）。
		s.writeJSONError(w, http.StatusServiceUnavailable, "index is stale, run rebuild-index")
	default:
		s.logf("web: %v", err)
		s.writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}

// ===== 需求-006：回收站（软删除 / 恢复 / 清除） =====

// maxActionBody 限制写接口请求体大小（只有路径/ID，远小于此）。
const maxActionBody = 4 << 10

// sameOriginOK 校验 Origin（或 Referer）与请求 Host 同源。
//
// 缺失该头时**放行**：非浏览器客户端不构成 CSRF 面（CSRF 依赖浏览器自动附带凭据），
// 而浏览器跨站 POST 已被 Cookie 的 SameSite=Lax 拦截（auth.go）。带该头但跨源
// 一律拒绝——作为纵深防御：将来若有人误把 SameSite 放宽，这层仍然有效。
func sameOriginOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// decodeBody 解析写接口的 JSON 请求体（限制大小）；失败时已写出响应并返回 false。
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActionBody)).Decode(dst); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "invalid json body")
		return false
	}
	return true
}

// handleTrashList 列出回收站（只读）。
func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListTrash()
	if err != nil {
		s.storeError(w, err)
		return
	}
	type item struct {
		ID        string `json:"id"`
		Path      string `json:"path"`
		DeletedAt string `json:"deleted_at"`
		Size      int64  `json:"size"`
	}
	out := make([]item, 0, len(entries))
	for _, e := range entries {
		out = append(out, item{
			ID:        e.ID,
			Path:      e.Path,
			DeletedAt: e.DeletedAt.Format("2006-01-02 15:04"),
			Size:      e.Size,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "entries": out})
}

// handleDelete 软删除：把文件移入回收站，并从索引移除（检索立即不再命中）。
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOriginOK(r) {
		s.writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	target := strings.TrimSpace(body.Path)
	if target == "" {
		s.writeJSONError(w, http.StatusBadRequest, "missing path")
		return
	}
	id, err := s.store.MoveToTrash(target)
	if err != nil {
		s.storeError(w, err)
		return
	}
	// 索引是派生状态：移除失败不阻断删除（下次 rebuild 会自愈），只记日志。
	if err := s.ix.Remove(filepath.ToSlash(target)); err != nil {
		if !errors.Is(err, index.ErrIndexStale) {
			s.logf("web: delete %q: moved to trash but index remove failed: %v", target, err)
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// handleRestore 从回收站恢复：移回原路径并重新索引。
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if !sameOriginOK(r) {
		s.writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	rel, err := memory.TrashOriginalPath(body.ID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if err := s.store.RestoreFromTrash(body.ID); err != nil {
		s.storeError(w, err)
		return
	}
	// 重新索引：失败只记日志（索引可再生，文件已恢复是既成事实）。
	if err := s.reindexPath(rel); err != nil {
		s.logf("web: restore %q: restored but reindex failed: %v", body.ID, err)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": rel})
}

// handlePurge 物理删除一条回收站条目（不可逆）。
func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	if !sameOriginOK(r) {
		s.writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if err := s.store.PurgeTrash(body.ID); err != nil {
		s.storeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTrashClear 清空回收站（不可逆）。
func (s *Server) handleTrashClear(w http.ResponseWriter, r *http.Request) {
	if !sameOriginOK(r) {
		s.writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	n, err := s.store.ClearTrash()
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": n})
}

// reindexPath 重新索引一个刚恢复的文件（索引是派生状态，读失败即返回错误由调用方记日志）。
func (s *Server) reindexPath(rel string) error {
	data, err := s.store.Read(rel)
	if err != nil {
		return err
	}
	meta, body := index.ParseFrontMatter(data)
	if mt, err := s.store.ModTime(rel); err == nil {
		meta.MTime = mt
	}
	meta.Size = int64(len(data))
	return s.ix.Upsert(filepath.ToSlash(rel), meta, []byte(body))
}
