package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/memory"
)

// staticFiles 是内嵌的静态资源（登录页、外壳页、脚本、样式）。
// 走 go:embed 而非外部 CDN：断网/墙内也能用，且不产生第三方请求。
//
//go:embed static
var staticFiles embed.FS

// Options 是阅读页的装配参数。
type Options struct {
	Store *memory.Store // 记忆库（与 MCP 共用同一实例，避免第二份状态）
	Index *index.Index  // 全文索引（同上）
	Token string        // 访问口令（由 cmd 从口令文件读入，永远不落日志）
	Logf  func(format string, args ...any)
}

// Server 是只读阅读页的处理器集合。
type Server struct {
	store  *memory.Store
	ix     *index.Index
	auth   *Authenticator
	logf   func(format string, args ...any)
	static http.Handler
	mux    *http.ServeMux
}

// New 装配路由与中间件。store / ix / token 均为必需项——缺任一项直接报错，
// 由 serve 快速失败，不留半配置的对外服务。
func New(opts Options) (*Server, error) {
	if opts.Store == nil || opts.Index == nil {
		return nil, errors.New("web: store and index are required")
	}
	if len(opts.Token) < MinTokenLen {
		return nil, fmt.Errorf("web: token too short (%d chars, want >= %d)", len(opts.Token), MinTokenLen)
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("web: embedded static files: %w", err)
	}

	s := &Server{
		store:  opts.Store,
		ix:     opts.Index,
		auth:   NewAuthenticator(opts.Token),
		logf:   logf,
		static: http.FileServer(http.FS(sub)),
	}

	mux := http.NewServeMux()
	// 公开路由：登录页与登录接口（本身不含任何库内容）。
	mux.HandleFunc("GET /memory/login", s.handleLoginPage)
	mux.HandleFunc("POST /memory/api/login", s.handleLogin)
	// 公开的站点图标：浏览器在**登录前**就会取标签页图标，未登录的登录页也没有 Cookie，
	// 故图标不走鉴权。只暴露这两个固定的图片文件（用户头像），不含任何库内容。
	favicon, err := embeddedIcon("static/icons/favicon.ico", "image/x-icon")
	if err != nil {
		return nil, err
	}
	touchIcon, err := embeddedIcon("static/icons/apple-touch-icon.png", "image/png")
	if err != nil {
		return nil, err
	}
	mux.HandleFunc("GET /memory/favicon.ico", favicon)
	mux.HandleFunc("GET /memory/apple-touch-icon.png", touchIcon)
	// 其余一律需口令。
	mux.Handle("POST /memory/api/logout", s.protected(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /memory/api/status", s.protected(http.HandlerFunc(s.handleStatus)))
	mux.Handle("GET /memory/api/tree", s.protected(http.HandlerFunc(s.handleTree)))
	mux.Handle("GET /memory/api/file", s.protected(http.HandlerFunc(s.handleFile)))
	mux.Handle("GET /memory/api/raw", s.protected(http.HandlerFunc(s.handleRaw)))
	mux.Handle("GET /memory/api/search", s.protected(http.HandlerFunc(s.handleSearch)))
	// 需求-006：回收站。读接口 GET，写接口**只接受 POST**——GET 有副作用的接口
	// 是 CSRF 的经典入口，务必保持此约束（对方即便构造 GET 链接也只会拿到 405）。
	mux.Handle("GET /memory/api/trash", s.protected(http.HandlerFunc(s.handleTrashList)))
	mux.Handle("POST /memory/api/delete", s.protected(http.HandlerFunc(s.handleDelete)))
	mux.Handle("POST /memory/api/restore", s.protected(http.HandlerFunc(s.handleRestore)))
	mux.Handle("POST /memory/api/purge", s.protected(http.HandlerFunc(s.handlePurge)))
	mux.Handle("POST /memory/api/trash/clear", s.protected(http.HandlerFunc(s.handleTrashClear)))
	// 批量操作：一次请求处理多条，逐条报告失败（部分成功不回滚）。
	mux.Handle("POST /memory/api/trash/purge-batch", s.protected(http.HandlerFunc(s.handleTrashPurgeBatch)))
	mux.Handle("POST /memory/api/trash/restore-batch", s.protected(http.HandlerFunc(s.handleTrashRestoreBatch)))
	// GET 兜底修正：下面 `GET /memory/` 是**子树模式**，会把写接口路径的 GET 请求
	// 抢成页面请求（最终 404），从而拿不到应有的 405。为每个写接口显式注册 GET
	// 处理器，保证「写接口只接受 POST」这条约束可被外部观测到（需求-006 AC6）。
	for _, p := range []string{
		"/memory/api/delete",
		"/memory/api/restore",
		"/memory/api/purge",
		"/memory/api/trash/clear",
		"/memory/api/trash/purge-batch",
		"/memory/api/trash/restore-batch",
	} {
		mux.Handle("GET "+p, s.protected(http.HandlerFunc(s.handleMethodNotAllowed)))
	}
	mux.Handle("GET /memory/static/", s.protected(http.StripPrefix("/memory/static/", s.static)))
	mux.Handle("GET /memory/", s.protected(http.HandlerFunc(s.handleIndexPage)))
	s.mux = mux
	return s, nil
}

// Handler 返回顶层 handler：访问日志（最外层，才能记到真实状态码）
// → 安全响应头 → panic 恢复 → 路由。
func (s *Server) Handler() http.Handler {
	return s.logRequests(s.secureHeaders(s.recoverer(s.mux)))
}

// handleMethodNotAllowed 对写接口的非 POST 请求返回 405 并声明 Allow。
func (s *Server) handleMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "POST")
	s.writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// embeddedIcon 把内嵌的图标文件包成一个只读处理器（公开路由用）。
// 图标是静态展示资源，单独给一天的可缓存头：默认的 no-store 面向含库内容的响应，
// 对图标既无必要，也会让每次开页面都重新拉一遍。
func embeddedIcon(name, contentType string) (http.HandlerFunc, error) {
	data, err := staticFiles.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("web: embedded icon %q: %w", name, err)
	}
	size := strconv.Itoa(len(data))
	return func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("Cache-Control", "public, max-age=86400")
		h.Set("Content-Length", size)
		_, _ = w.Write(data)
	}, nil
}

// protected 包装需口令的路由：凭据有效放行；否则浏览器导航跳登录页、
// 其余请求返回 401（不泄露任何库内容）。
func (s *Server) protected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth.Authenticate(r) {
			next.ServeHTTP(w, r)
			return
		}
		if wantsHTML(r) {
			http.Redirect(w, r, "/memory/login", http.StatusFound)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="zipper-memory"`)
		s.writeJSONError(w, http.StatusUnauthorized, "unauthorized")
	})
}

// wantsHTML 报告请求是否来自浏览器顶层导航（Accept 含 text/html）。
// 页面 fetch 一律显式带 Accept: application/json，故不会被误判。
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// statusRecorder 记录实际写出的状态码，供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

// logRequests 记录访问日志：只记方法、路径、状态、来源与耗时——
// **不记 query 与 Cookie**，避免检索词与口令进入日志；路径先经
// [sanitizeLogField] 消毒，防止 `%0a` 之类的控制字符伪造日志行（log forging）。
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logf("web: %s %s %d %s %s", r.Method, sanitizeLogField(r.URL.Path), rec.status, clientIP(r),
			time.Since(start).Round(time.Millisecond))
	})
}

// sanitizeLogField 把控制字符（含 CR/LF/NUL）替换为 '?'，其余原样保留。
// 用途：日志字段的注入防护——Go 会把 `%0a` 解码进 URL.Path，未消毒即可伪造
// 整行日志（例如伪造一条 200 的"已授权访问"记录，污染事后审计）。
func sanitizeLogField(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

// secureHeaders 为所有响应补充基础安全头。
// Cache-Control: no-store —— 响应可能含库原文（正文、下载、检索片段），
// 一律不进浏览器缓存与中间缓存。
func (s *Server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// recoverer 把 handler 内的 panic 转成 500，避免单个请求打挂整个进程
// （MCP 与该服务同进程，必须隔离）。
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logf("web: panic on %s: %v", r.URL.Path, rec)
				s.writeJSONError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// serveStatic 输出内嵌的静态文件（HTML）。
func (s *Server) serveStatic(w http.ResponseWriter, name string) {
	data, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		s.logf("web: read embedded %s: %v", name, err)
		s.writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(data); err != nil {
		s.logf("web: write %s: %v", name, err)
	}
}

// writeJSON 输出 JSON 响应（不缓存：内容含库原文）。
func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logf("web: encode json response: %v", err)
	}
}

// writeJSONError 输出错误响应：只给简短原因，不暴露路径与文件系统细节。
func (s *Server) writeJSONError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}
