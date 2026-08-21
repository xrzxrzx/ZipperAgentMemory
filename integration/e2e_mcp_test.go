//go:build e2e

// Package integration 是阶段 3 的端到端验证（真实进程 + 双传输）。
//
// 运行方式（需 Go 工具链在 PATH）：
//
//	cd D:\Works\ZipperAgentMemory
//	go test -tags e2e ./integration/ -v
//
// 覆盖：构建真实二进制 zipper-agent-memoryd，分别以 stdio 与
// serve（streamable HTTP）两种模式拉起进程，走完整 MCP 握手
// （initialize → tools/list → tools/call），验证 6 个工具、路径穿越
// 拒绝与中文检索。测试不进入 go test ./...（CI 之外手动可跑，编码规范 §8.3）。
package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// repoRoot 返回仓库根目录（本测试所在目录的上一级）。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// buildBinary 用 go build 构建真实二进制到临时目录，返回其路径。
func buildBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "zipper-agent-memoryd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/zipper-agent-memoryd")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// memoryRoot 创建含中文内容样本的临时记忆库。
func memoryRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"README.md":                   "# 记忆库\n示例入口",
		"notes/go-mcp-development.md": "Go 语言开发笔记：模型上下文协议与记忆工具设计",
		"notes/meeting.md":            "今日会议记录：无语言相关主题，讨论部署计划",
		"structured/contacts.csv":     "name,email\nalice,alice@example.com",
		"agent/test-agent/2026-08.md": "本月沉淀：阶段 3 交付 MCP 服务器",
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
	return root
}

// freePort 挑一个空闲端口（serve 模式 e2e 用）。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// ---- 通用 JSON-RPC 消息 ----

type rpcMsg struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method,omitempty"`
	Params  any    `json:"params,omitempty"`
}

func initParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-client", "version": "0.1.0"},
	}
}

// ---- stdio 模式 ----

// TestE2EStdio 以真实二进制 stdio 模式完成握手 + 6 工具调用 + 路径穿越拒绝。
func TestE2EStdio(t *testing.T) {
	bin := buildBinary(t)
	root := memoryRoot(t)

	cmd := exec.Command(bin, "stdio", "-root", root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	br := bufio.NewReader(stdout)

	// 握手：initialize
	res := rpcCall(t, br, stdin, 1, "initialize", initParams())
	si := res["serverInfo"].(map[string]any)
	if si["name"] != "zipper-agent-memoryd" {
		t.Fatalf("serverInfo.name = %v", si["name"])
	}
	// notifications/initialized（标准流程）
	if err := writeRPC(stdin, 0, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatal(err)
	}

	// tools/list：6 个工具齐全
	res = rpcCall(t, br, stdin, 2, "tools/list", map[string]any{})
	tools := res["tools"].([]any)
	if len(tools) != 6 {
		t.Fatalf("tools 数量 = %d，期望 6：%v", len(tools), tools)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"memory_read", "memory_write", "memory_append", "memory_search", "memory_list", "memory_status"} {
		if !names[want] {
			t.Errorf("tools/list 缺少 %s", want)
		}
	}

	// tools/call：memory_status
	res = rpcCall(t, br, stdin, 3, "tools/call", map[string]any{"name": "memory_status", "arguments": map[string]any{}})
	if txt := resultText(res); !strings.Contains(txt, "files: 5") {
		t.Errorf("memory_status = %q，期望 files: 5", txt)
	}

	// memory_write + memory_read 回读
	rpcCall(t, br, stdin, 4, "tools/call", map[string]any{"name": "memory_write", "arguments": map[string]any{
		"path": "notes/e2e.md", "content": "e2e 写入内容",
	}})
	res = rpcCall(t, br, stdin, 5, "tools/call", map[string]any{"name": "memory_read", "arguments": map[string]any{"path": "notes/e2e.md"}})
	if txt := resultText(res); txt != "e2e 写入内容" {
		t.Errorf("memory_read = %q", txt)
	}

	// memory_append + memory_search 中文命中（写后索引同步，无需等 watcher）
	rpcCall(t, br, stdin, 6, "tools/call", map[string]any{"name": "memory_append", "arguments": map[string]any{
		"path": "notes/e2e.md", "content": "追加的检索关键词：并发写",
	}})
	res = rpcCall(t, br, stdin, 7, "tools/call", map[string]any{"name": "memory_search", "arguments": map[string]any{"query": "并发写"}})
	if txt := resultText(res); !strings.Contains(txt, "notes/e2e.md") {
		t.Errorf("中文检索未命中 notes/e2e.md：%q", txt)
	}

	// memory_list
	res = rpcCall(t, br, stdin, 8, "tools/call", map[string]any{"name": "memory_list", "arguments": map[string]any{}})
	if txt := resultText(res); !strings.Contains(txt, "notes/") {
		t.Errorf("memory_list 缺 notes/：%q", txt)
	}

	// 路径穿越：IsError=true 且提示路径穿越
	res = rpcCall(t, br, stdin, 9, "tools/call", map[string]any{"name": "memory_read", "arguments": map[string]any{"path": "../secret.md"}})
	if !resultIsError(res) || !strings.Contains(resultText(res), "escapes memory root") {
		t.Errorf("路径穿越应返回 IsError 工具错误：%v", res)
	}

	_ = stderr // 日志留作诊断
}

// ---- serve（streamable HTTP）模式 ----

// TestE2EHTTP 以真实二进制 serve 模式（streamable HTTP）完成握手 +
// tools/list + 6 工具调用。
func TestE2EHTTP(t *testing.T) {
	bin := buildBinary(t)
	root := memoryRoot(t)
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s/mcp", addr)

	cmd := exec.Command(bin, "serve", "-root", root, "-addr", addr)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	waitReady(t, addr)

	sessID := ""
	post := func(id int, method string, params any) map[string]any {
		t.Helper()
		b, err := json.Marshal(rpcMsg{JSONRPC: "2.0", ID: id, Method: method, Params: params})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", url, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessID != "" {
			req.Header.Set("Mcp-Session-Id", sessID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", method, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode == http.StatusAccepted {
			return nil // MCP 通知的合法响应（HTTP 202 Accepted，无 body）
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s: HTTP %d: %s", method, resp.StatusCode, body)
		}
		if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
			sessID = id
		}
		var r struct {
			Result map[string]any `json:"result"`
			Error  any            `json:"error"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("POST %s: 响应非 JSON：%s", method, body)
		}
		if r.Error != nil {
			t.Fatalf("POST %s 返回错误：%v", method, r.Error)
		}
		return r.Result
	}

	// 握手
	res := post(1, "initialize", initParams())
	si := res["serverInfo"].(map[string]any)
	if si["name"] != "zipper-agent-memoryd" {
		t.Fatalf("serverInfo.name = %v", si["name"])
	}
	post(0, "notifications/initialized", map[string]any{})

	// tools/list：6 个
	res = post(2, "tools/list", map[string]any{})
	if tools := res["tools"].([]any); len(tools) != 6 {
		t.Fatalf("tools 数量 = %d，期望 6", len(tools))
	}

	// 6 个工具逐个调用
	post(3, "tools/call", map[string]any{"name": "memory_write", "arguments": map[string]any{"path": "notes/http.md", "content": "HTTP 模式写入"}})
	res = post(4, "tools/call", map[string]any{"name": "memory_read", "arguments": map[string]any{"path": "notes/http.md"}})
	if txt := resultText(res); txt != "HTTP 模式写入" {
		t.Errorf("HTTP memory_read = %q", txt)
	}
	post(5, "tools/call", map[string]any{"name": "memory_append", "arguments": map[string]any{"path": "notes/http.md", "content": "追加中文：语言学习"}})
	res = post(6, "tools/call", map[string]any{"name": "memory_search", "arguments": map[string]any{"query": "语言"}})
	if txt := resultText(res); !strings.Contains(txt, "notes/http.md") {
		t.Errorf("HTTP 中文检索未命中：%q", txt)
	}
	res = post(7, "tools/call", map[string]any{"name": "memory_list", "arguments": map[string]any{}})
	if txt := resultText(res); !strings.Contains(txt, "notes/") {
		t.Errorf("HTTP memory_list 缺 notes/：%q", txt)
	}
	res = post(8, "tools/call", map[string]any{"name": "memory_status", "arguments": map[string]any{}})
	if txt := resultText(res); !strings.Contains(txt, "files:") {
		t.Errorf("HTTP memory_status = %q", txt)
	}

	// 路径穿越拒绝
	res = post(9, "tools/call", map[string]any{"name": "memory_read", "arguments": map[string]any{"path": "../../etc/passwd"}})
	if !resultIsError(res) {
		t.Errorf("HTTP 路径穿越应返回 IsError：%v", res)
	}

	_ = stderr
}

// ---- 辅助 ----

func writeRPC(w io.Writer, id int, method string, params any) error {
	b, err := json.Marshal(rpcMsg{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// rpcCall 写一行 JSON-RPC 请求并读回一行响应；协议错误直接 fatal。
func rpcCall(t *testing.T, r *bufio.Reader, w io.Writer, id int, method string, params any) map[string]any {
	t.Helper()
	if err := writeRPC(w, id, method, params); err != nil {
		t.Fatal(err)
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("读取 %s 响应: %v", method, err)
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("解析 %s 响应 %q: %v", method, line, err)
	}
	if resp.Error != nil {
		t.Fatalf("%s 返回协议错误：%v", method, resp.Error)
	}
	return resp.Result
}

// resultText 从 tools/call 结果取 TextContent 文本。
func resultText(res map[string]any) string {
	content, _ := res["content"].([]any)
	var b strings.Builder
	for _, c := range content {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "text" {
			b.WriteString(m["text"].(string))
		}
	}
	return b.String()
}

// resultIsError 判断 tools/call 结果 isError 标志。
func resultIsError(res map[string]any) bool {
	b, _ := res["isError"].(bool)
	return b
}

// waitReady 轮询 TCP 直到 serve 进程可连（最多 10s）。
func waitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("serve 进程 %s 未就绪", addr)
}
