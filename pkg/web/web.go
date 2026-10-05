package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
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
	// 其余一律需口令。
	mux.Handle("POST /memory/api/logout", s.protected(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /memory/api/status", s.protected(http.HandlerFunc(s.handleStatus)))
	mux.Handle("GET /memory/api/tree", s.protected(http.HandlerFunc(s.handleTree)))
	mux.Handle("GET /memory/api/file", s.protected(http.HandlerFunc(s.handleFile)))
	mux.Handle("GET /memory/api/raw", s.protected(http.HandlerFunc(s.handleRaw)))
	mux.Handle("GET /memory/api/search", s.protected(http.HandlerFunc(s.handleSearch)))
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

// protected 包装需口令的路由：凭据有效放行；否则浏览器导航跳登录页、
// 其余请求返回 401（不泄露任何库内容）。
func (s *Server) protected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth.Valid(s.auth.Credential(r)) {
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
// **不记 query 与 Cookie**，避免检索词与口令进入日志。
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logf("web: %s %s %d %s %s", r.Method, r.URL.Path, rec.status, clientIP(r),
			time.Since(start).Round(time.Millisecond))
	})
}

// secureHeaders 为所有响应补充基础安全头。
func (s *Server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
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
