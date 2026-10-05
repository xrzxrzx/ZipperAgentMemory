package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/memory"
)

// lockedBuffer 是并发安全的日志缓冲（handler 在自己的 goroutine 里写）。
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testEnv 是一次测试用的完整环境：临时记忆库 + 索引 + 已启动的 httptest server。
type testEnv struct {
	http  *httptest.Server
	store *memory.Store
	ix    *index.Index
	logs  *lockedBuffer
	root  string
}

const testToken = "test-token-0123456789abcdefghijklmn"

// newTestEnv 装配环境并写入若干测试文件（同时重建索引）。
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	store, err := memory.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close() })

	files := map[string]string{
		"notes/both.md":   "# 标题\n\n隧道 与 端口 都出现，含 `代码` 与表格。\n",
		"notes/tunnel.md": "# 隧道\n\n只有隧道。\n",
		"notes/port.md":   "# 端口\n\n只有端口。\n",
		"notes/plain.txt": "<script>alert('xss')</script>\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ix.Rebuild(store); err != nil {
		t.Fatal(err)
	}

	logs := &lockedBuffer{}
	srv, err := New(Options{Store: store, Index: ix, Token: testToken,
		Logf: func(format string, args ...any) { fmt.Fprintf(logs, format+"\n", args...) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{http: ts, store: store, ix: ix, logs: logs, root: root}
}

// do 发一个请求；token 为空表示不带凭据，accept 为空表示默认头。
func (e *testEnv) do(t *testing.T, method, path, token, accept string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.http.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // 便于断言 302 本身
	}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func decodeJSON(t *testing.T, res *http.Response, out any) {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

// ---- 口令文件 ----

func TestTokenFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-token")

	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if len(token) != 43 {
		t.Errorf("token 长度 = %d，want 43（32 字节 base64url）", len(token))
	}
	again, err := GenerateToken()
	if err != nil || again == token {
		t.Errorf("两次生成的口令应不同（err=%v）", err)
	}

	if err := WriteToken(path, token); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("口令文件权限 = %#o，want 0600", perm)
		}
	}
	loaded, err := LoadToken(path)
	if err != nil || loaded != token {
		t.Errorf("LoadToken = %q, %v；want %q", loaded, err, token)
	}

	// 缺失文件与过短口令都要报错（快速失败，不带病运行）。
	if _, err := LoadToken(filepath.Join(dir, "nope")); err == nil {
		t.Error("缺失的口令文件应报错")
	}
	short := filepath.Join(dir, "short")
	if err := os.WriteFile(short, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(short); err == nil {
		t.Error("过短口令应报错")
	}
}

// TestTokenFilePermissionCheck 仅在类 Unix 上生效（Windows 无等价语义）。
func TestTokenFilePermissionCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限语义")
	}
	path := filepath.Join(t.TempDir(), "web-token")
	if err := os.WriteFile(path, []byte(testToken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Error("0644 的口令文件应被拒绝")
	}
}

// ---- 认证与限流 ----

func TestAuthenticatorCredentialAndValidity(t *testing.T) {
	a := NewAuthenticator(testToken)

	// 无凭据 → 拒绝。
	if a.Authenticate(httptest.NewRequest(http.MethodGet, "/memory/api/status", nil)) {
		t.Error("无凭据不应通过")
	}
	// Bearer 正确。
	req := httptest.NewRequest(http.MethodGet, "/memory/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if !a.Authenticate(req) {
		t.Error("Bearer 正确应通过")
	}
	// Cookie 正确。
	req2 := httptest.NewRequest(http.MethodGet, "/memory/api/status", nil)
	req2.AddCookie(&http.Cookie{Name: CookieName, Value: testToken})
	if !a.Authenticate(req2) {
		t.Error("Cookie 正确应通过")
	}
	// Bearer 已失效 + Cookie 有效 → 仍应通过（客户端残留的失效 Bearer 不得顶掉会话）。
	req3 := httptest.NewRequest(http.MethodGet, "/memory/api/status", nil)
	req3.Header.Set("Authorization", "Bearer stale-token-stale-token-stale")
	req3.AddCookie(&http.Cookie{Name: CookieName, Value: testToken})
	if !a.Authenticate(req3) {
		t.Error("Bearer 失效时不应顶掉有效 Cookie")
	}
	// Bearer 与 Cookie 都无效 → 拒绝。
	req4 := httptest.NewRequest(http.MethodGet, "/memory/api/status", nil)
	req4.Header.Set("Authorization", "Bearer stale-token-stale-token-stale")
	req4.AddCookie(&http.Cookie{Name: CookieName, Value: "stale-cookie-stale-cookie-stale"})
	if a.Authenticate(req4) {
		t.Error("两路凭据都失效不应通过")
	}

	if !a.Valid(testToken) {
		t.Error("正确口令应通过")
	}
	for _, bad := range []string{"", "wrong", testToken + "x", testToken[:len(testToken)-1]} {
		if a.Valid(bad) {
			t.Errorf("错误口令 %q 不应通过", bad)
		}
	}
}

func TestAuthenticatorRateLimit(t *testing.T) {
	a := NewAuthenticator(testToken)
	clock := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return clock }

	ip := "10.0.0.9"
	for i := 0; i < failLimit; i++ {
		if !a.Allowed(ip) {
			t.Fatalf("第 %d 次尝试不应被拒", i+1)
		}
		a.Fail(ip)
	}
	if a.Allowed(ip) {
		t.Errorf("连续 %d 次失败后应被限流", failLimit)
	}
	// 其他来源不受影响。
	if !a.Allowed("10.0.0.10") {
		t.Error("限流应按来源 IP 隔离")
	}
	// 窗口过期后恢复。
	clock = clock.Add(failWindow + time.Second)
	if !a.Allowed(ip) {
		t.Error("窗口过期后应恢复")
	}
	// 成功登录清零计数。
	a.Fail(ip)
	a.Succeed(ip)
	for i := 0; i < failLimit-1; i++ {
		a.Fail(ip)
	}
	if !a.Allowed(ip) {
		t.Error("Succeed 应清零失败计数")
	}
}

// ---- 渲染 ----

// TestRenderMarkdownEscapesRawHTML 安全验收：库内（外部抓取的）资料含原始
// HTML/脚本时，渲染结果绝不可执行。
//
// goldmark 的默认行为是**省略**原始 HTML 并留下注释标记（不是转义为实体）——
// 断言因此按真实语义写：不得出现可执行标签/属性，且必须留下省略标记。
func TestRenderMarkdownEscapesRawHTML(t *testing.T) {
	src := "# 标题\n\n<script>alert('xss')</script>\n\n<img src=x onerror=alert(1)>\n"
	out, err := RenderMarkdown([]byte(src))
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	lower := strings.ToLower(out)
	for _, bad := range []string{"<script", "<img", "onerror=", "alert("} {
		if strings.Contains(lower, bad) {
			t.Errorf("渲染结果含可执行内容 %q：%s", bad, out)
		}
	}
	if !strings.Contains(out, "raw HTML omitted") {
		t.Errorf("原始 HTML 应被省略并留标记，实际：%s", out)
	}
	if !strings.Contains(out, "标题") {
		t.Errorf("正常 Markdown 内容应保留，实际：%s", out)
	}
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		rel  string
		data []byte
		want Kind
	}{
		{"notes/a.md", []byte("中文"), KindMarkdown},
		{"notes/a.markdown", []byte("中文"), KindMarkdown},
		{"notes/a.txt", []byte("纯文本"), KindText},
		{"structured/x.csv", []byte("a,b\n1,2\n"), KindText},
		{"notes/bin.dat", []byte{'a', 0, 'b'}, KindBinary},
	}
	for _, c := range cases {
		if got := KindOf(c.rel, c.data); got != c.want {
			t.Errorf("KindOf(%q) = %v，want %v", c.rel, got, c.want)
		}
	}
}

// ---- 访问控制 ----

func TestProtectedAccessMatrix(t *testing.T) {
	env := newTestEnv(t)

	// API 无凭据 → 401。
	if res := env.do(t, http.MethodGet, "/memory/api/status", "", "application/json"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("无凭据 API = %d，want 401", res.StatusCode)
	}
	// 错误口令 → 401。
	if res := env.do(t, http.MethodGet, "/memory/api/status", "wrong-token-wrong-token-wrong", "application/json"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("错误口令 API = %d，want 401", res.StatusCode)
	}
	// 正确口令 → 200。
	if res := env.do(t, http.MethodGet, "/memory/api/status", testToken, "application/json"); res.StatusCode != http.StatusOK {
		t.Errorf("正确口令 API = %d，want 200", res.StatusCode)
	}
	// 浏览器导航无凭据 → 302 到登录页。
	res := env.do(t, http.MethodGet, "/memory/", "", "text/html,application/xhtml+xml")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("未登录导航 = %d，want 302", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/memory/login" {
		t.Errorf("跳转目标 = %q，want /memory/login", loc)
	}
	// 登录页公开可访问，且不含任何库内容。
	res = env.do(t, http.MethodGet, "/memory/login", "", "text/html")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("登录页 = %d，want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "只有隧道") {
		t.Error("登录页不应包含库内容")
	}
	// 未授权时静态资源同样不可读。
	if res := env.do(t, http.MethodGet, "/memory/static/app.js", "", "text/html"); res.StatusCode != http.StatusFound {
		t.Errorf("未授权静态资源 = %d，want 302", res.StatusCode)
	}
}

// TestPathTraversalRejected 验收：路径穿越一律被拒绝，且不泄露根外内容。
//
// 注意平台差异：`..` 系列在 Linux/Windows 上都返回 400（沙箱直接判定越界）；
// 绝对路径（/etc/passwd）在 Linux 上是 400，在 Windows 上 IsAbs 为 false，
// 会被当作"根内不存在的文件"返回 404——两者都是拒绝，故断言"非 200 且无内容泄露"。
func TestPathTraversalRejected(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		path    string
		status  int
		exactly bool
	}{
		{"../../etc/passwd", http.StatusBadRequest, true},
		{"..%2F..%2Fetc%2Fpasswd", http.StatusBadRequest, true},
		{"notes/../../secret.txt", http.StatusBadRequest, true},
		{"/etc/passwd", http.StatusBadRequest, false}, // Windows 上为 404，同样安全
	}
	for _, c := range cases {
		res := env.do(t, http.MethodGet, "/memory/api/file?path="+c.path, testToken, "application/json")
		if c.exactly && res.StatusCode != c.status {
			t.Errorf("穿越路径 %q = %d，want %d", c.path, res.StatusCode, c.status)
			continue
		}
		if !c.exactly && res.StatusCode == http.StatusOK {
			t.Errorf("绝对路径 %q 不应返回 200", c.path)
			continue
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "root:") {
			t.Errorf("穿越请求 %q 泄露了根外内容：%s", c.path, body)
		}
	}
}

// ---- 端到端 ----

// TestEndToEnd 走完「登录 → 目录 → 正文 → 检索 → 下载」全链路。
func TestEndToEnd(t *testing.T) {
	env := newTestEnv(t)

	// 1. 登录换 Cookie。
	loginBody := strings.NewReader(`{"token":"` + testToken + `"}`)
	req, err := http.NewRequest(http.MethodPost, env.http.URL+"/memory/api/login", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("登录 = %d，want 200", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("登录响应未下发 Cookie")
	}
	if !cookie.HttpOnly {
		t.Error("Cookie 应为 HttpOnly")
	}
	if cookie.MaxAge != CookieMaxAge {
		t.Errorf("Cookie MaxAge = %d，want %d（不过期）", cookie.MaxAge, CookieMaxAge)
	}
	// httptest 走明文 HTTP，Secure 位不应置位（否则浏览器丢弃 Cookie）。
	if cookie.Secure {
		t.Error("明文 HTTP 下 Cookie 不应置 Secure")
	}

	// 2. 带 Cookie 访问目录。
	req, err = http.NewRequest(http.MethodGet, env.http.URL+"/memory/api/tree?path=notes", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var tree treeResponse
	decodeJSON(t, res, &tree)
	if len(tree.Entries) == 0 {
		t.Fatalf("目录为空：%+v", tree)
	}

	// 3. 正文（Markdown 渲染）。
	res = env.do(t, http.MethodGet, "/memory/api/file?path=notes%2Fboth.md", testToken, "application/json")
	var file fileResponse
	decodeJSON(t, res, &file)
	if file.Kind != string(KindMarkdown) || !strings.Contains(file.HTML, "<h1") {
		t.Errorf("Markdown 渲染异常：kind=%s html=%q", file.Kind, file.HTML)
	}
	if file.MTime == "" {
		t.Error("应返回 mtime")
	}

	// 4. 文本文件按原文返回（前端用 textContent 写入，不做二次转义）。
	res = env.do(t, http.MethodGet, "/memory/api/file?path=notes%2Fplain.txt", testToken, "application/json")
	var plain fileResponse
	decodeJSON(t, res, &plain)
	if plain.Kind != string(KindText) || !strings.Contains(plain.Text, "<script>") {
		t.Errorf("文本类型应返回原文：kind=%s text=%q", plain.Kind, plain.Text)
	}

	// 5. 检索：AND 只命中同时含两词的文件，OR 命中三个。
	res = env.do(t, http.MethodGet, "/memory/api/search?q=%E9%9A%A7%E9%81%93%20%E7%AB%AF%E5%8F%A3&mode=and", testToken, "application/json")
	var andResp searchResponse
	decodeJSON(t, res, &andResp)
	if andResp.Count != 1 || andResp.Hits[0].Path != "notes/both.md" {
		t.Errorf("AND 检索 = %+v，want 仅 notes/both.md", andResp)
	}
	res = env.do(t, http.MethodGet, "/memory/api/search?q=%E9%9A%A7%E9%81%93%20%E7%AB%AF%E5%8F%A3&mode=or", testToken, "application/json")
	var orResp searchResponse
	decodeJSON(t, res, &orResp)
	if orResp.Count != 3 {
		t.Errorf("OR 检索 = %d 条，want 3", orResp.Count)
	}
	if orResp.Mode != "or" {
		t.Errorf("响应应回显模式，实际 %q", orResp.Mode)
	}
	// 非法模式与非法 limit → 400。
	if res := env.do(t, http.MethodGet, "/memory/api/search?q=x&mode=xor", testToken, "application/json"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 mode = %d，want 400", res.StatusCode)
	}
	if res := env.do(t, http.MethodGet, "/memory/api/search?q=x&limit=0", testToken, "application/json"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 limit = %d，want 400", res.StatusCode)
	}
	if res := env.do(t, http.MethodGet, "/memory/api/search?q=%20%20", testToken, "application/json"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("空查询 = %d，want 400", res.StatusCode)
	}

	// 6. 下载：字节与磁盘文件逐字节一致。
	want, err := os.ReadFile(filepath.Join(env.root, "notes", "both.md"))
	if err != nil {
		t.Fatal(err)
	}
	res = env.do(t, http.MethodGet, "/memory/api/raw?path=notes%2Fboth.md", testToken, "application/json")
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("下载内容与源文件不一致：%d vs %d 字节", len(got), len(want))
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("下载应带 attachment，实际 %q", cd)
	}
}

// TestLoginRateLimitedOverHTTP 验收：错误口令连续尝试触发 429。
func TestLoginRateLimitedOverHTTP(t *testing.T) {
	env := newTestEnv(t)
	var last int
	for i := 0; i < failLimit+2; i++ {
		req, err := http.NewRequest(http.MethodPost, env.http.URL+"/memory/api/login",
			strings.NewReader(`{"token":"wrong-token-wrong-token-wrong"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		last = res.StatusCode
		_ = res.Body.Close()
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("连续失败后的状态码 = %d，want 429", last)
	}
}

// TestLogsDoNotLeakQueryOrToken 安全验收：访问日志不得包含检索词与口令。
func TestLogsDoNotLeakQueryOrToken(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, http.MethodGet, "/memory/api/search?q=SuperSecretKeyword&mode=and", testToken, "application/json")

	logs := env.logs.String()
	if strings.Contains(logs, "SuperSecretKeyword") {
		t.Errorf("日志泄露检索词：%s", logs)
	}
	if strings.Contains(logs, testToken) {
		t.Errorf("日志泄露口令：%s", logs)
	}
	if !strings.Contains(logs, "/memory/api/search") {
		t.Errorf("日志应记录路径，实际：%s", logs)
	}
}

// TestIndexPageShell 验收：登录后 /memory/ 返回外壳页，其他子路径 404。
func TestIndexPageShell(t *testing.T) {
	env := newTestEnv(t)
	res := env.do(t, http.MethodGet, "/memory/", testToken, "text/html")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/memory/ 带凭据 = %d，want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/memory/static/app.js") {
		t.Error("外壳页应引用内嵌脚本")
	}
	if res := env.do(t, http.MethodGet, "/memory/unknown", testToken, "text/html"); res.StatusCode != http.StatusNotFound {
		t.Errorf("/memory/unknown = %d，want 404", res.StatusCode)
	}
}

// TestLoginPageSelfContained 验收（独立验证 P2-1 修复）：公开登录页必须自带样式，
// **不得外链** `/memory/static/*`——静态资源需认证（design.md §11.4），
// 浏览器取样式表时发 `Accept: text/css`，会被 401 拦掉，导致登录页无样式裸奔。
func TestLoginPageSelfContained(t *testing.T) {
	env := newTestEnv(t)
	res := env.do(t, http.MethodGet, "/memory/login", "", "text/html")
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	// 精确判定「外链」：注释里提到该路径属文档说明，不算引用。
	for _, ref := range []string{`<link`, `href="/memory/static/`, `src="/memory/static/`} {
		if strings.Contains(page, ref) {
			t.Errorf("登录页不应外链需认证的静态资源（命中 %q）", ref)
		}
	}
	if !strings.Contains(page, "<style>") {
		t.Error("登录页应内联样式")
	}
	// 反向确认：真实浏览器取样式表的 Accept 头确实拿不到静态资源（设计如此）。
	if res := env.do(t, http.MethodGet, "/memory/static/style.css", "", "text/css,*/*;q=0.1"); res.StatusCode == http.StatusOK {
		t.Error("静态资源不应在未授权时可读（若放宽了认证声明，需同步 design.md §11.4）")
	}
}

// TestClientInputErrorsAre4xx 验收（独立验证 P2-2 修复）：客户端可构造的输入
// 错误（目录路径、超长路径、超长分量、含 NUL）一律 4xx，不得下沉成 5xx。
func TestClientInputErrorsAre4xx(t *testing.T) {
	env := newTestEnv(t)
	bad := []string{
		"notes",                   // 目录
		".",                       // 根目录
		"notes/",                  // 目录（尾斜杠）
		strings.Repeat("a", 300),  // 单级分量超长
		strings.Repeat("a", 5000), // 整串超长
		"a%00b",                   // 含 NUL
		"a%01b",                   // 其它控制字符
	}
	for _, p := range bad {
		res := env.do(t, http.MethodGet, "/memory/api/file?path="+p, testToken, "application/json")
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("file?path=%q = %d，want 400", p, res.StatusCode)
		}
		res = env.do(t, http.MethodGet, "/memory/api/raw?path="+p, testToken, "application/json")
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("raw?path=%q = %d，want 400", p, res.StatusCode)
		}
	}
	// 对照：合法路径仍 200（证明上面的拒绝不是"一律拒绝"）。
	if res := env.do(t, http.MethodGet, "/memory/api/file?path=notes%2Fboth.md", testToken, "application/json"); res.StatusCode != http.StatusOK {
		t.Errorf("合法路径 = %d，want 200", res.StatusCode)
	}
	// 不存在的文件仍 404。
	if res := env.do(t, http.MethodGet, "/memory/api/file?path=notes%2Fnope.md", testToken, "application/json"); res.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的文件 = %d，want 404", res.StatusCode)
	}
}

// TestLogForgingSanitized 验收（独立验证 P2-3 修复）：URL 中的控制字符不得在
// 访问日志里伪造出整行记录。
func TestLogForgingSanitized(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, http.MethodGet, "/memory/api/nonexistent%0aINJECTED%20LOG%20LINE", "", "application/json")
	logs := env.logs.String()
	if strings.Contains(logs, "\nINJECTED") {
		t.Errorf("日志被换行注入：%q", logs)
	}
	if !strings.Contains(logs, "?INJECTED") {
		t.Errorf("控制字符应被替换为 '?'，实际：%q", logs)
	}
}

// TestResponsesNotCached 验收（独立验证 P3-3 修复）：含库内容的响应不得被缓存。
func TestResponsesNotCached(t *testing.T) {
	env := newTestEnv(t)
	for _, p := range []string{"/memory/api/status", "/memory/api/raw?path=notes%2Fboth.md", "/memory/static/app.js"} {
		res := env.do(t, http.MethodGet, p, testToken, "application/json")
		if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("%s 的 Cache-Control = %q，want 含 no-store", p, cc)
		}
	}
}
