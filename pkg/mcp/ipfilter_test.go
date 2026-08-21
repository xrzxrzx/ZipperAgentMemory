package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// passthrough 是中间件测试的下游 handler：返回 200 OK。
func passthrough() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestIPAllowListAllowed 验收：Allowed 判定（命中/未命中/空白名单/解析失败）。
func TestIPAllowListAllowed(t *testing.T) {
	l, err := NewIPAllowList([]string{"1.2.3.4", "::1"})
	if err != nil {
		t.Fatalf("NewIPAllowList: %v", err)
	}
	cases := []struct {
		name   string
		remote string
		want   bool
	}{
		{"命中 IPv4（host:port）", "1.2.3.4:5678", true},
		{"命中 IPv4（裸 IP，防御性）", "1.2.3.4", true},
		{"未命中 IPv4", "1.2.3.5:5678", false},
		{"命中 IPv6（[::1]:port）", "[::1]:5678", true},
		{"未命中 IPv6", "[::2]:5678", false},
		{"IPv4-mapped IPv6 归一为 IPv4 命中", "[::ffff:1.2.3.4]:9999", true},
		{"无法解析的远端 → 拒绝（fail closed）", "not-an-ip:80", false},
		{"空远端 → 拒绝", "", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := l.Allowed(tt.remote); got != tt.want {
				t.Errorf("Allowed(%q) = %v, want %v", tt.remote, got, tt.want)
			}
		})
	}

	// 空白名单：恒放行（本地模式无感知）。
	empty, err := NewIPAllowList(nil)
	if err != nil {
		t.Fatalf("NewIPAllowList(nil): %v", err)
	}
	if !empty.Empty() {
		t.Fatal("nil 列表应为空白名单")
	}
	for _, remote := range []string{"127.0.0.1:8931", "9.9.9.9:1", "garbage"} {
		if !empty.Allowed(remote) {
			t.Errorf("空白名单应放行 %q", remote)
		}
	}
}

// TestIPAllowListMiddleware 验收：中间件行为（放行 200 / 未命中 403 /
// 空白名单全放行 / IPv6）。
func TestIPAllowListMiddleware(t *testing.T) {
	// 空白名单：任何远端都放行（含 127.0.0.1 本地模式）。
	empty, err := NewIPAllowList(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := empty.Middleware(passthrough())
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("空白名单应放行，got %d", rec.Code)
	}

	// 白名单 ["1.2.3.4", "2001:db8::1"]。
	l, err := NewIPAllowList([]string{"1.2.3.4", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	mw := l.Middleware(passthrough())

	cases := []struct {
		name   string
		remote string
		want   int
	}{
		{"命中 IPv4 → 200", "1.2.3.4:1111", http.StatusOK},
		{"未命中 IPv4 → 403", "9.9.9.9:1111", http.StatusForbidden},
		{"命中 IPv6 → 200", "[2001:db8::1]:2222", http.StatusOK},
		{"未命中 IPv6 → 403", "[2001:db8::2]:2222", http.StatusForbidden},
		{"无法解析远端 → 403", "nonsense:80", http.StatusForbidden},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.RemoteAddr = tt.remote
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("remote %q: got HTTP %d, want %d", tt.remote, rec.Code, tt.want)
			}
		})
	}
}

// TestIPAllowListNewValidation 验收：非法 IP 条目在构造时即报错（快速失败）。
func TestIPAllowListNewValidation(t *testing.T) {
	for _, bad := range []string{"1.2.3.4.5", "abc", "", "1.2.3", "256.1.1.1"} {
		if _, err := NewIPAllowList([]string{bad}); err == nil {
			t.Errorf("NewIPAllowList(%q) 应报错", bad)
		}
	}
	// 合法条目混合非法条目：整体失败（不留半配置）。
	if _, err := NewIPAllowList([]string{"1.2.3.4", "oops"}); err == nil {
		t.Error("混合非法条目应整体失败")
	}
}
