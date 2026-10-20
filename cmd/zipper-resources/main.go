// Command zipper-resources 是记忆库的**只读共享资源服务**（需求-007）。
//
// 它把内网磁盘上的二进制资源（头像、壁纸、安装包…）以只读 HTTP 暴露给内网
// 的 AI 与工具，并提供 /api/list 清单供记忆库生成可检索的条目。
//
// 设计要点：
//   - **只读**：没有任何上传/删除接口；新增资源靠 SCP 到目录后自动发现；
//   - **不经过 MCP**：大文件走本服务 HTTP，MCP 只承载清单元数据（避免撑爆 JSON-RPC）；
//   - **sha256 缓存**：以 (size, mtime) 为键，未变则复用，避免每次请求重算全量哈希；
//   - **仅内网**：监听 0.0.0.0:<port>，由 Windows 防火墙限制到内网网段（与 Gitea 同款做法）。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry 是清单中的一条资源。
type Entry struct {
	ID     string `json:"id"`     // 相对 root 的路径（正斜杠），即访问路径
	Name   string `json:"name"`   // 文件名
	Size   int64  `json:"size"`   // 字节数
	SHA256 string `json:"sha256"` // 内容哈希，供取用方自校验
	MIME   string `json:"mime"`   // 推测的媒体类型
	MTime  string `json:"mtime"`  // 文件修改时间（RFC3339）
}

// Manifest 是 /api/list 的响应体。
type Manifest struct {
	Root    string  `json:"root"`
	Count   int     `json:"count"`
	Updated string  `json:"updated"`
	Entries []Entry `json:"entries"`
}

// cacheItem 是 sha256 缓存项（键为资源 ID）。
type cacheItem struct {
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"`
	SHA256 string `json:"sha256"`
}

// extraMIME 补齐常见但 Go 内置表可能缺失的扩展名。
var extraMIME = map[string]string{
	".webp": "image/webp",
	".exe":  "application/vnd.microsoft.portable-executable",
	".msi":  "application/x-msi",
	".7z":   "application/x-7z-compressed",
	".zip":  "application/zip",
	".apk":  "application/vnd.android.package-archive",
}

type server struct {
	root      string
	cachePath string
	mu        sync.Mutex
	cache     map[string]cacheItem
}

func main() {
	root := flag.String("root", `D:\ZipperResources`, "资源根目录")
	addr := flag.String("addr", "0.0.0.0:8940", "监听地址（仅内网，请用防火墙限制网段）")
	cachePath := flag.String("cache", "", "sha256 缓存文件（默认在 root 同级）")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatalf("resources: resolve root: %v", err)
	}
	if fi, err := os.Stat(absRoot); err != nil || !fi.IsDir() {
		log.Fatalf("resources: root %q is not a directory", absRoot)
	}
	if *cachePath == "" {
		*cachePath = filepath.Join(filepath.Dir(absRoot), ".zipper-resources.cache.json")
	}

	s := &server{root: absRoot, cachePath: *cachePath, cache: map[string]cacheItem{}}
	s.loadCache()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/list", s.handleList)
	mux.HandleFunc("GET /{id...}", s.handleFile)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("resources: serving %s on http://%s (cache=%s)", absRoot, *addr, *cachePath)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("resources: serve: %v", err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleList 扫描 root 并返回清单。sha256 走缓存，只有 (size, mtime) 变化才重算。
func (s *server) handleList(w http.ResponseWriter, _ *http.Request) {
	entries, err := s.scan()
	if err != nil {
		log.Printf("resources: scan: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed"})
		return
	}
	writeJSON(w, http.StatusOK, Manifest{
		Root:    s.root,
		Count:   len(entries),
		Updated: time.Now().Format(time.RFC3339),
		Entries: entries,
	})
}

// handleFile 只读地返回单个资源。路径必须落在 root 内（拒绝穿越）。
func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.PathValue("id"), "/")
	if id == "" || strings.HasPrefix(id, "api/") {
		http.NotFound(w, r)
		return
	}
	// 归一化并做穿越校验：Clean 后仍必须以 root 为前缀。
	clean := filepath.Clean(filepath.FromSlash(id))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	target := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	if ct := mimeOf(target); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// 资源内容按内容哈希天然可缓存，但为与阅读页保持一致仍禁掉中间缓存。
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, target)
}

// scan 遍历 root 生成清单（跳过点文件与缓存文件本身）。
func (s *server) scan() ([]Entry, error) {
	entries := make([]Entry, 0, 16)
	err := filepath.WalkDir(s.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil // 点文件（含缓存）不登记
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		id := filepath.ToSlash(rel)
		sum, err := s.hashFor(p, id, fi)
		if err != nil {
			return err
		}
		entries = append(entries, Entry{
			ID:     id,
			Name:   name,
			Size:   fi.Size(),
			SHA256: sum,
			MIME:   mimeOf(name),
			MTime:  fi.ModTime().Format(time.RFC3339),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	s.saveCache()
	return entries, nil
}

// hashFor 返回资源的 sha256；命中缓存且 (size, mtime) 未变时直接复用。
func (s *server) hashFor(fullPath, id string, fi os.FileInfo) (string, error) {
	s.mu.Lock()
	cached, ok := s.cache[id]
	s.mu.Unlock()
	if ok && cached.Size == fi.Size() && cached.MTime == fi.ModTime().UnixNano() {
		return cached.SHA256, nil
	}
	sum, err := fileSHA256(fullPath)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.cache[id] = cacheItem{Size: fi.Size(), MTime: fi.ModTime().UnixNano(), SHA256: sum}
	s.mu.Unlock()
	return sum, nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *server) loadCache() {
	data, err := os.ReadFile(s.cachePath)
	if err != nil {
		return
	}
	var m map[string]cacheItem
	if err := json.Unmarshal(data, &m); err == nil && m != nil {
		s.cache = m
	}
}

func (s *server) saveCache() {
	s.mu.Lock()
	data, err := json.MarshalIndent(s.cache, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(s.cachePath, data, 0o644)
}

// mimeOf 按扩展名推测媒体类型，补齐内置表缺项。
func mimeOf(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ct, ok := extraMIME[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("resources: encode response: %v", err)
	}
}
