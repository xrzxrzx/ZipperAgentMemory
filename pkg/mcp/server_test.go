package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/memory"
)

// newTestServer 创建临时记忆库 + 索引 + MCP server，并经 SDK 官方内存传输
// （NewInMemoryTransports）建立客户端会话——走完整协议栈（initialize 握手 +
// tools/list + tools/call），与真实客户端等价。
func newTestServer(t *testing.T) (*Server, *sdk.ClientSession, string) {
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

	s := New(store, ix)

	t1, t2 := sdk.NewInMemoryTransports()
	if _, err := s.sdk.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "v0.1"}, nil)
	sess, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return s, sess, root
}

// callTool 调用工具并返回结果（协议错误直接 fatal）。
func callTool(t *testing.T, sess *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

// textOf 拼接结果全部 TextContent 文本。
func textOf(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestListToolsRegistered 验收：tools/list 含全部 6 个工具，行为标注正确。
func TestListToolsRegistered(t *testing.T) {
	_, sess, _ := newTestServer(t)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]*sdk.Tool, len(res.Tools))
	for _, tl := range res.Tools {
		got[tl.Name] = tl
	}
	want := []string{"memory_read", "memory_write", "memory_append", "memory_search", "memory_list", "memory_status"}
	if len(got) != len(want) {
		t.Fatalf("工具数 = %d，期望 %d：%v", len(got), len(want), res.Tools)
	}
	for _, name := range want {
		tl, ok := got[name]
		if !ok {
			t.Fatalf("缺少工具 %q", name)
		}
		if tl.InputSchema == nil {
			t.Errorf("%s: InputSchema 缺失", name)
		}
		if tl.Annotations == nil {
			t.Fatalf("%s: 行为标注（Annotations）缺失", name)
		}
		if tl.Annotations.OpenWorldHint == nil || *tl.Annotations.OpenWorldHint {
			t.Errorf("%s: OpenWorldHint 应为 false（记忆库闭域）", name)
		}
	}
	// 行为标注核对（design.md §6.1）
	for _, name := range []string{"memory_read", "memory_search", "memory_list", "memory_status"} {
		if !got[name].Annotations.ReadOnlyHint {
			t.Errorf("%s: 应为 readOnlyHint=true", name)
		}
		if got[name].Annotations.DestructiveHint != nil && *got[name].Annotations.DestructiveHint {
			t.Errorf("%s: 只读工具不应标 destructiveHint", name)
		}
	}
	wr := got["memory_write"].Annotations
	if wr.DestructiveHint == nil || !*wr.DestructiveHint || !wr.IdempotentHint {
		t.Errorf("memory_write: 应 destructive+idempotent，实际 %+v", wr)
	}
	ap := got["memory_append"].Annotations
	if ap.DestructiveHint == nil || !*ap.DestructiveHint || ap.IdempotentHint {
		t.Errorf("memory_append: 应 destructive 非 idempotent，实际 %+v", ap)
	}
	if got["memory_read"].Annotations.ReadOnlyHint != true {
		t.Errorf("memory_read: 应 readOnly")
	}
}

// TestReadWriteAppend 验收：memory_read/write/append 功能正确（真实 memory/）。
func TestReadWriteAppend(t *testing.T) {
	_, sess, _ := newTestServer(t)

	// write 新建
	res := callTool(t, sess, "memory_write", map[string]any{"path": "notes/hello.md", "content": "hello world"})
	if res.IsError {
		t.Fatalf("write 新建失败: %s", textOf(res))
	}
	// read 读回
	res = callTool(t, sess, "memory_read", map[string]any{"path": "notes/hello.md"})
	if res.IsError || textOf(res) != "hello world" {
		t.Fatalf("read 读回 = %q (isError=%v)", textOf(res), res.IsError)
	}
	// write 不覆盖：存在即报错（默认）
	res = callTool(t, sess, "memory_write", map[string]any{"path": "notes/hello.md", "content": "overwrite?"})
	if !res.IsError || !strings.Contains(textOf(res), "already exists") {
		t.Fatalf("write 已存在应报错，实际 %q (isError=%v)", textOf(res), res.IsError)
	}
	// write 覆盖
	res = callTool(t, sess, "memory_write", map[string]any{"path": "notes/hello.md", "content": "v2", "overwrite": true})
	if res.IsError {
		t.Fatalf("write 覆盖失败: %s", textOf(res))
	}
	// append
	res = callTool(t, sess, "memory_append", map[string]any{"path": "notes/hello.md", "content": "appended line"})
	if res.IsError {
		t.Fatalf("append 失败: %s", textOf(res))
	}
	res = callTool(t, sess, "memory_read", map[string]any{"path": "notes/hello.md"})
	body := textOf(res)
	if !strings.Contains(body, "v2") || !strings.Contains(body, "appended line") || !strings.Contains(body, "appended ") {
		t.Fatalf("append 后内容 = %q", body)
	}
	// 缺 path 参数：结构化错误
	res = callTool(t, sess, "memory_read", map[string]any{})
	if !res.IsError || !strings.Contains(textOf(res), "path") {
		t.Fatalf("缺 path 应报错，实际 %q", textOf(res))
	}
}

// TestPathTraversalRejected 验收：路径穿越返回错误（../、绝对路径）。
func TestPathTraversalRejected(t *testing.T) {
	_, sess, _ := newTestServer(t)

	evil := []string{"../secret.md", "notes/../../secret.md", ".."}
	// Windows 用绝对盘符路径；Unix 用 /etc/passwd（resolve 拒绝绝对路径）
	abs := "/etc/passwd"
	if runtime.GOOS == "windows" {
		abs = `C:\Windows\win.ini`
	}
	evil = append(evil, abs)

	for _, p := range evil {
		res := callTool(t, sess, "memory_read", map[string]any{"path": p})
		if !res.IsError || !strings.Contains(textOf(res), "escapes memory root") {
			t.Errorf("memory_read(%q) 应拒绝（IsError=true 且提示路径穿越），实际 %q", p, textOf(res))
		}
		res = callTool(t, sess, "memory_write", map[string]any{"path": p, "content": "x"})
		if !res.IsError {
			t.Errorf("memory_write(%q) 应拒绝", p)
		}
		res = callTool(t, sess, "memory_append", map[string]any{"path": p, "content": "x"})
		if !res.IsError {
			t.Errorf("memory_append(%q) 应拒绝", p)
		}
		res = callTool(t, sess, "memory_list", map[string]any{"path": p})
		if !res.IsError {
			t.Errorf("memory_list(%q) 应拒绝", p)
		}
	}
}

// createJunction 强制用 cmd mklink /J 创建目录联接（Windows 无需管理员），
// 用于符号链接/junction 逃逸覆盖；非 Windows 返回错误由调用方跳过。
func createJunction(t *testing.T, link, target string) error {
	t.Helper()
	if runtime.GOOS != "windows" {
		return errors.New("junction 仅 Windows 支持")
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, absTarget)
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.New(string(out))
	}
	return nil
}

// TestSymlinkEscapeRejected 验收：符号链接/junction 逃逸被拒绝（MCP 层）。
// 根内 junction 指向根外目录，memory_read/write 均应返回路径穿越错误。
func TestSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "f.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "evil")
	if err := createJunction(t, link, outside); err != nil {
		t.Skipf("无法创建 junction，跳过：%v", err)
	}
	store, err := memory.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	s := New(store, ix)
	t1, t2 := sdk.NewInMemoryTransports()
	if _, err := s.sdk.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "v0.1"}, nil)
	sess, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	res := callTool(t, sess, "memory_read", map[string]any{"path": "evil/f.md"})
	if !res.IsError || !strings.Contains(textOf(res), "escapes memory root") {
		t.Errorf("junction 逃逸 read 应拒绝，实际 %q", textOf(res))
	}
	res = callTool(t, sess, "memory_write", map[string]any{"path": "evil/new.md", "content": "x"})
	if !res.IsError {
		t.Errorf("junction 逃逸 write 应拒绝")
	}
}

// TestSearchChineseHit 验收：memory_search 中文关键词命中（与索引 CJK 切分一致）。
func TestSearchChineseHit(t *testing.T) {
	s, sess, _ := newTestServer(t)
	files := map[string]string{
		"notes/go-mcp-development.md": "Go 语言开发笔记：模型上下文协议与记忆工具设计",
		"notes/meeting.md":            "今日会议记录：无语言相关主题，讨论部署计划",
		"projects/alpha/readme.md":    "项目 Alpha：Go 服务端架构（英文内容 go language）",
	}
	for rel, content := range files {
		if err := s.store.Write(rel, []byte(content), false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ix.Rebuild(s.store); err != nil {
		t.Fatal(err)
	}

	// 中文关键词命中
	res := callTool(t, sess, "memory_search", map[string]any{"query": "语言"})
	if res.IsError {
		t.Fatalf("search 失败: %s", textOf(res))
	}
	body := textOf(res)
	if !strings.Contains(body, "go-mcp-development.md") {
		t.Errorf("中文查询“语言”应命中 go-mcp-development.md，实际:\n%s", body)
	}
	if !strings.Contains(body, "语言") {
		t.Errorf("命中片段应含中文关键词，实际:\n%s", body)
	}

	// limit 生效
	res = callTool(t, sess, "memory_search", map[string]any{"query": "语言", "limit": 1})
	if res.IsError || !strings.HasPrefix(textOf(res), "1 hit(s)") {
		t.Errorf("limit=1 应只返回 1 条，实际:\n%s", textOf(res))
	}

	// 空查询报错
	res = callTool(t, sess, "memory_search", map[string]any{"query": "  "})
	if !res.IsError {
		t.Errorf("空查询应报错")
	}
}

// TestConcurrentAppendNoLoss 验收：并发写不丢数据（并发 append 全部落盘）。
func TestConcurrentAppendNoLoss(t *testing.T) {
	_, sess, _ := newTestServer(t)
	const (
		workers = 10
		each    = 10
	)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				marker := fmt.Sprintf("line-%d-%d", g, i)
				res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
					Name:      "memory_append",
					Arguments: map[string]any{"path": "agent/conc.md", "content": marker},
				})
				if err != nil {
					errs <- err
					return
				}
				if res.IsError {
					errs <- errors.New(textOf(res))
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发 append 出错: %v", err)
	}

	// 经 memory_read 工具读回，核对全部 marker 无丢失
	res := callTool(t, sess, "memory_read", map[string]any{"path": "agent/conc.md"})
	body := textOf(res)
	if res.IsError {
		t.Fatalf("read 失败: %s", body)
	}
	for g := 0; g < workers; g++ {
		for i := 0; i < each; i++ {
			marker := fmt.Sprintf("line-%d-%d", g, i)
			if !strings.Contains(body, marker) {
				t.Errorf("并发 append 丢失 marker %s", marker)
			}
		}
	}
}

// TestListAndStatus 验收：memory_list / memory_status 功能正确。
func TestListAndStatus(t *testing.T) {
	s, sess, _ := newTestServer(t)
	// 空库 status
	res := callTool(t, sess, "memory_status", nil)
	if res.IsError || !strings.Contains(textOf(res), "files: 0") {
		t.Fatalf("空库 status = %q", textOf(res))
	}
	// 写两个文件（一个在子目录）
	for _, f := range []string{"a.md", "sub/b.md"} {
		if err := s.store.Write(f, []byte("content"), false); err != nil {
			t.Fatal(err)
		}
	}
	res = callTool(t, sess, "memory_status", nil)
	body := textOf(res)
	if !strings.Contains(body, "files: 2") || !strings.Contains(body, "bytes: 14") {
		t.Fatalf("status = %q，期望 files: 2 bytes: 14", body)
	}
	// list 根目录
	res = callTool(t, sess, "memory_list", nil)
	body = textOf(res)
	if !strings.Contains(body, "a.md") || !strings.Contains(body, "sub/") {
		t.Fatalf("list 根 = %q", body)
	}
	// list 子目录
	res = callTool(t, sess, "memory_list", map[string]any{"path": "sub"})
	if textOf(res) != "b.md\n" {
		t.Fatalf("list sub = %q", textOf(res))
	}
	// list 不存在目录
	res = callTool(t, sess, "memory_list", map[string]any{"path": "nope"})
	if !res.IsError {
		t.Fatalf("list 不存在目录应报错")
	}
}

// TestUnknownTool 验证未知工具返回协议级错误。
func TestUnknownTool(t *testing.T) {
	_, sess, _ := newTestServer(t)
	_, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: "no_such_tool"})
	if err == nil {
		t.Fatal("未知工具应返回错误")
	}
}

// TestDecodeArgsInvalidJSON 参数解析边界：非 JSON / 语法错误 / 类型错误的
// 参数载荷一律报错（decodeArgs 是全部工具参数校验的公共入口）。
func TestDecodeArgsInvalidJSON(t *testing.T) {
	var out struct{ Path string }
	for _, raw := range []json.RawMessage{
		json.RawMessage(`not json`),
		json.RawMessage(`{"path": }`),
		json.RawMessage(`[1,2,3]`),  // 数组而非对象
		json.RawMessage(`"string"`), // 字符串而非对象
	} {
		if err := decodeArgs("t", raw, &out); err == nil {
			t.Errorf("decodeArgs(%q) 应报错", raw)
		}
	}
	if err := decodeArgs("t", json.RawMessage(`{"path":"a.md"}`), &out); err != nil {
		t.Fatalf("decodeArgs(valid) = %v", err)
	}
	if out.Path != "a.md" {
		t.Errorf("Path = %q, want a.md", out.Path)
	}
	// 空/缺失参数按 {} 处理（不报错，缺参语义由各 handler 判断）。
	if err := decodeArgs("t", nil, &out); err != nil {
		t.Errorf("decodeArgs(nil) = %v", err)
	}
}

// TestToolArgumentTypeValidation 参数校验：错误类型的参数值（number/array/
// boolean 代替 string）返回 IsError 工具错误，而不是协议崩溃。
func TestToolArgumentTypeValidation(t *testing.T) {
	_, sess, _ := newTestServer(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"memory_read", map[string]any{"path": 123}},
		{"memory_write", map[string]any{"path": "notes/x.md", "content": 456}},
		{"memory_append", map[string]any{"path": 789, "content": "x"}},
		{"memory_search", map[string]any{"query": []string{"x"}}},
		{"memory_list", map[string]any{"path": true}},
	}
	for _, c := range cases {
		res := callTool(t, sess, c.name, c.args)
		if !res.IsError {
			t.Errorf("%s 错误类型参数应返回 IsError 工具错误，实际 %q", c.name, textOf(res))
		}
	}
}

// TestSearchNegativeLimitDefaults 边界：search limit<=0 按默认 20 处理
// （不报错、不 panic），与 pkg/index.Search 语义一致。
func TestSearchNegativeLimitDefaults(t *testing.T) {
	s, sess, _ := newTestServer(t)
	if err := s.store.Write("notes/boundary.md", []byte("边界测试 boundary keyword"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ix.Rebuild(s.store); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, sess, "memory_search", map[string]any{"query": "boundary", "limit": -1})
	if res.IsError {
		t.Fatalf("limit=-1 应正常返回，实际 %q", textOf(res))
	}
	if !strings.HasPrefix(textOf(res), "1 hit(s)") {
		t.Errorf("limit=-1 应命中 1 条，实际 %q", textOf(res))
	}
}

// TestListDefaultPath 边界：memory_list 缺省 path 与 "." 等价（列根目录），
// 与工具 schema 默认值一致。
func TestListDefaultPath(t *testing.T) {
	s, sess, _ := newTestServer(t)
	if err := s.store.Write("rootfile.md", []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, sess, "memory_list", map[string]any{})
	if res.IsError || !strings.Contains(textOf(res), "rootfile.md") {
		t.Errorf("缺省 path 应列根目录，实际 %q", textOf(res))
	}
	res = callTool(t, sess, "memory_list", map[string]any{"path": "."})
	if res.IsError || !strings.Contains(textOf(res), "rootfile.md") {
		t.Errorf("path=. 应列根目录，实际 %q", textOf(res))
	}
}
