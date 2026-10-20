package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/memory"
)

const testTok = "0123456789abcdef0123456789abcdef"

// newTrashServer 组装一个最小可用的阅读页（真实 store + 真实索引 + 临时目录）。
func newTrashServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("# A\n\nhello 世界"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := memory.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "idx.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	srv, err := New(Options{Store: store, Index: ix, Token: testTok})
	if err != nil {
		t.Fatal(err)
	}
	return srv, root
}

// trashReq 发一个带鉴权的请求；origin 为空则不带 Origin 头。
func trashReq(srv *Server, method, path string, body any, origin string, withAuth bool) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withAuth {
		req.Header.Set("Authorization", "Bearer "+testTok)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestTrashEndpoints 软删除 → 回收站可见 → 恢复 的完整闭环（AC1/AC2/AC3）。
func TestTrashEndpoints(t *testing.T) {
	srv, root := newTrashServer(t)

	rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": "notes/a.md"}, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d, want 200；body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "a.md")); !os.IsNotExist(err) {
		t.Fatal("删除后原文件应不存在")
	}

	rec = trashReq(srv, http.MethodGet, "/memory/api/trash", nil, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("trash list = %d, want 200", rec.Code)
	}
	var list struct {
		Count   int `json:"count"`
		Entries []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("解析回收站响应: %v", err)
	}
	if list.Count != 1 || list.Entries[0].Path != "notes/a.md" {
		t.Fatalf("回收站内容不符：%s", rec.Body.String())
	}

	rec = trashReq(srv, http.MethodPost, "/memory/api/restore", map[string]string{"id": list.Entries[0].ID}, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore = %d, want 200；body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "a.md")); err != nil {
		t.Fatalf("恢复后文件应存在: %v", err)
	}
}

// TestTrashWriteMethodsOnlyPost 写接口只接受 POST，GET 一律 405（AC6）。
func TestTrashWriteMethodsOnlyPost(t *testing.T) {
	srv, _ := newTrashServer(t)
	for _, p := range []string{"/memory/api/delete", "/memory/api/restore", "/memory/api/purge", "/memory/api/trash/clear"} {
		rec := trashReq(srv, http.MethodGet, p, nil, "", true)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", p, rec.Code)
		}
	}
}

// TestTrashRequiresAuth 未登录一律 401，且不产生副作用（AC7）。
func TestTrashRequiresAuth(t *testing.T) {
	srv, root := newTrashServer(t)
	rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": "notes/a.md"}, "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录 delete = %d, want 401", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "a.md")); err != nil {
		t.Fatal("未登录请求不应产生副作用（文件应仍在）")
	}
}

// TestTrashCrossOriginRejected 跨源 Origin 一律 403（AC8）。
func TestTrashCrossOriginRejected(t *testing.T) {
	srv, root := newTrashServer(t)
	rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": "notes/a.md"}, "https://evil.example", true)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨源 delete = %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "a.md")); err != nil {
		t.Fatal("跨源请求不应产生副作用")
	}
}

// TestTrashSameOriginAllowed 同源 Origin 应放行（httptest 的 Host 为 example.com）。
func TestTrashSameOriginAllowed(t *testing.T) {
	srv, _ := newTrashServer(t)
	rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": "notes/a.md"}, "http://example.com", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("同源 delete = %d, want 200；body=%s", rec.Code, rec.Body.String())
	}
}

// TestTrashNotReachableByGenericAPIs .trash 不得经 tree/file/raw 访问（AC11）。
func TestTrashNotReachableByGenericAPIs(t *testing.T) {
	srv, _ := newTrashServer(t)
	if rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": "notes/a.md"}, "", true); rec.Code != http.StatusOK {
		t.Fatalf("准备回收站失败：%d", rec.Code)
	}
	for _, p := range []string{
		"/memory/api/tree?path=.",
		"/memory/api/tree?path=.trash",
	} {
		if rec := trashReq(srv, http.MethodGet, p, nil, "", true); rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), ".trash") {
			t.Errorf("通用 tree 不应暴露 .trash：%s", p)
		}
	}
	for _, p := range []string{
		"/memory/api/file?path=.trash",
		"/memory/api/raw?path=.trash",
	} {
		if rec := trashReq(srv, http.MethodGet, p, nil, "", true); rec.Code == http.StatusOK {
			t.Errorf("通用接口不应可读 .trash：%s -> %d", p, rec.Code)
		}
	}
}

// TestTrashPurgeAndClearEndpoints 清除与清空接口（AC5）。
func TestTrashPurgeAndClearEndpoints(t *testing.T) {
	srv, root := newTrashServer(t)
	if err := os.WriteFile(filepath.Join(root, "notes", "b.md"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"notes/a.md", "notes/b.md"} {
		if rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": p}, "", true); rec.Code != http.StatusOK {
			t.Fatalf("删除 %s 失败：%d", p, rec.Code)
		}
	}
	rec := trashReq(srv, http.MethodGet, "/memory/api/trash", nil, "", true)
	var list struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Entries) != 2 {
		t.Fatalf("应有 2 条，实际 %d", len(list.Entries))
	}

	rec = trashReq(srv, http.MethodPost, "/memory/api/purge", map[string]string{"id": list.Entries[0].ID}, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge = %d, want 200", rec.Code)
	}
	rec = trashReq(srv, http.MethodPost, "/memory/api/trash/clear", map[string]string{}, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear = %d, want 200", rec.Code)
	}
	rec = trashReq(srv, http.MethodGet, "/memory/api/trash", nil, "", true)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Entries) != 0 {
		t.Fatalf("清空后应为 0 条，实际 %d", len(list.Entries))
	}
}

// TestTrashDeleteOutsideRootRejected 软删除同样受沙箱约束（AC9）。
func TestTrashDeleteOutsideRootRejected(t *testing.T) {
	srv, _ := newTrashServer(t)
	rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": "../../etc/passwd"}, "", true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("越界删除 = %d, want 400；body=%s", rec.Code, rec.Body.String())
	}
}

// TestTrashDeleteDirectory 目录连同其全部内容进回收站，可整体恢复（A 方案）。
func TestTrashDeleteDirectory(t *testing.T) {
	srv, root := newTrashServer(t)
	if err := os.WriteFile(filepath.Join(root, "notes", "b.md"), []byte("# B"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := trashReq(srv, http.MethodPost, "/memory/api/delete", map[string]string{"path": "notes"}, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete dir = %d, want 200；body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "notes")); !os.IsNotExist(err) {
		t.Fatal("目录应已整体移走")
	}

	rec = trashReq(srv, http.MethodGet, "/memory/api/trash", nil, "", true)
	var list struct {
		Count   int `json:"count"`
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("解析回收站: %v", err)
	}
	if list.Count != 2 {
		t.Fatalf("回收站应含 2 个文件，实际 %d", list.Count)
	}

	// 用目录条目（id 形如 <批次>/notes）整体恢复
	dirID := strings.Split(list.Entries[0].ID, "/")[0] + "/notes"
	rec = trashReq(srv, http.MethodPost, "/memory/api/restore", map[string]string{"id": dirID}, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore dir = %d, want 200；body=%s", rec.Code, rec.Body.String())
	}
	for _, n := range []string{"a.md", "b.md"} {
		if _, err := os.Stat(filepath.Join(root, "notes", n)); err != nil {
			t.Errorf("恢复后 %s 应存在: %v", n, err)
		}
	}
}
