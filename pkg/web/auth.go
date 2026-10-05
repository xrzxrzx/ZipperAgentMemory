package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 口令、Cookie 与限流常量。
const (
	// CookieName 是登录成功后下发的 Cookie 名。
	CookieName = "zam_token"
	// CookieMaxAge 是 Cookie 有效期（秒）：10 年——对应「口令不过期」的需求。
	CookieMaxAge = 10 * 365 * 24 * 60 * 60
	// MinTokenLen 是接受的最短口令长度（32 字节 base64url 为 43 字符）。
	MinTokenLen = 24
	// tokenBytes 是新口令的随机字节数。
	tokenBytes = 32

	// failLimit / failWindow 组成按远端 IP 的登录失败限流：窗口内失败达到
	// failLimit 次即拒绝后续尝试（HTTP 429），防止在线暴力猜解。
	failLimit  = 10
	failWindow = time.Minute
	// maxFailEntries 限制限流表规模，防止伪造大量源地址造成内存膨胀。
	maxFailEntries = 1024
)

// ErrTokenFileUnsafe 表示口令文件权限过于宽松（同组或其他用户可访问）。
var ErrTokenFileUnsafe = errors.New("web: token file permissions too open (want 0600)")

// LoadToken 从 path 读入口令：取首尾空白内的内容并校验长度；类 Unix 平台
// 还要求权限不含 group/other 位（Windows 无等价语义，跳过该校验）。
func LoadToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("web: read token file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < MinTokenLen {
		return "", fmt.Errorf("web: token in %s too short (%d chars, want >= %d)", path, len(token), MinTokenLen)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("web: stat token file %s: %w", path, err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			return "", fmt.Errorf("%w: %s is %#o", ErrTokenFileUnsafe, path, perm)
		}
	}
	return token, nil
}

// GenerateToken 生成新口令：32 字节随机数的 base64url 编码（43 字符，URL 安全）。
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("web: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// WriteToken 以 0600 原子写入口令文件（同目录临时文件 → rename），
// 供首次生成与泄露后轮换使用。
func WriteToken(path, token string) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".web-token-*")
	if err != nil {
		return fmt.Errorf("web: create temp token file in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // rename 成功后此处为 no-op，失败时清理残留

	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return fmt.Errorf("web: write temp token file %s: %w", tmp, err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("web: chmod temp token file %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("web: close temp token file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("web: rename token file to %s: %w", path, err)
	}
	return nil
}

// failState 记录某来源 IP 在限流窗口内的失败次数。
type failState struct {
	count int
	start time.Time
}

// Authenticator 校验口令并实施按远端 IP 的失败限流。
// 并发安全：所有计数访问都经 mu 串行化。
type Authenticator struct {
	token string
	now   func() time.Time // 便于测试注入时钟

	mu    sync.Mutex
	fails map[string]*failState
}

// NewAuthenticator 用给定口令构造校验器。
func NewAuthenticator(token string) *Authenticator {
	return &Authenticator{token: token, now: time.Now, fails: make(map[string]*failState)}
}

// Authenticate 校验请求携带的口令：Bearer 有效即通过；Bearer 存在但无效时
// **继续检查 Cookie**（避免客户端残留的过期 Bearer 头把有效会话顶掉）；
// 两者都无效返回 false。
func (a *Authenticator) Authenticate(r *http.Request) bool {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		if a.Valid(strings.TrimSpace(h[7:])) {
			return true
		}
	}
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	return a.Valid(c.Value)
}

// Valid 以常量时间比较口令，避免逐字符比较泄露信息。
func (a *Authenticator) Valid(cred string) bool {
	if cred == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cred), []byte(a.token)) == 1
}

// Allowed 报告 ip 当前是否仍可尝试登录（超过失败阈值返回 false → 调用方回 429）。
func (a *Authenticator) Allowed(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.fails[ip]
	if !ok {
		return true
	}
	if a.now().Sub(st.start) > failWindow {
		delete(a.fails, ip)
		return true
	}
	return st.count < failLimit
}

// Fail 记录一次登录失败（窗口过期则重新计数）。
func (a *Authenticator) Fail(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if st, ok := a.fails[ip]; ok && now.Sub(st.start) <= failWindow {
		st.count++
	} else {
		a.fails[ip] = &failState{count: 1, start: now}
	}
	if len(a.fails) > maxFailEntries {
		for k, st := range a.fails {
			if now.Sub(st.start) > failWindow {
				delete(a.fails, k)
			}
		}
	}
}

// Succeed 登录成功后清除该 IP 的失败计数。
func (a *Authenticator) Succeed(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fails, ip)
}

// SetAuthCookie 下发登录 Cookie（持久，对应「口令不过期」）。
// Secure 位按实际传输判定：TLS 直连或 nginx 反代（X-Forwarded-Proto: https）
// 才置位——否则本机 HTTP 调试时浏览器会直接丢弃 Cookie，页面无法登录。
func SetAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/memory/",
		MaxAge:   CookieMaxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearAuthCookie 清除登录 Cookie（属性与下发时保持一致，确保同键覆盖）。
func ClearAuthCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/memory/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// isHTTPS 报告请求是否经 HTTPS 抵达（直连看 TLS，反代看 X-Forwarded-Proto）。
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP 取客户端 IP（失败限流按它分桶）。
//
// 反代取舍（2026-10-05 用户决策）：本服务只监听回环，线上请求全部经同机 nginx
// 抵达，此时 RemoteAddr 恒为 127.0.0.1。若直接用它，全互联网来源会被聚合进
// **同一个限流桶**——任意可达者连发 10 次错误口令即可让所有者 60 秒内无法新登录。
//
// 规则：**仅当 RemoteAddr 本身是回环地址时**才采信反代写入的来源头；非回环来源
// 一律用自己的 RemoteAddr（此时它无法伪造，伪造的 X-Real-IP / X-Forwarded-For
// 被忽略）。来源头优先级：`X-Real-IP`（nginx 用 `proxy_set_header` 覆写，不可伪造）
// → `X-Forwarded-For` 的**最后一段**（nginx 用 `$proxy_add_x_forwarded_for` 追加，
// 最后一段才是本机观测到的真实来源；取最左等于让攻击者随便填）→ 回环地址本身。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isLoopbackHost(host) {
		return host
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(ip) != nil {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(last) != nil {
			return last
		}
	}
	return host
}

// isLoopbackHost 报告主机名是否为回环地址（含 IPv4/IPv6 与 localhost）。
func isLoopbackHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}
