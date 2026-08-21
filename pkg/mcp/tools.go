package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"zipper-agent-memory/pkg/memory"
)

// 本文件实现 6 个 memory_* 工具的注册与 handler（docs/design.md §6.1）：
//
//	memory_read    只读：读取文件内容
//	memory_write   破坏性+幂等：写入/新建文件（overwrite=false 时存在即报错）
//	memory_append  破坏性：追加内容（自动加时间戳分隔行）
//	memory_search  只读：FTS5 全文检索（中文按单字切分，与索引侧一致）
//	memory_list    只读：列出目录项
//	memory_status  只读：记忆库统计
//
// 错误语义：工具执行错误（路径穿越、文件不存在、参数非法等）一律以
// CallToolResult.IsError=true + Content 文本表达（MCP 规范推荐，LLM 可见
// 并自纠正），不上升为 JSON-RPC 协议错误。
//
// 健壮性：所有 handler 经 withRecovery 包装，panic 转为工具错误响应。

// registerTools 注册全部 6 个工具。AddTool 的 handler 参数为 SDK ToolHandler；
// 参数校验、路径沙箱、panic 恢复都在这一层完成。
func (s *Server) registerTools(sdkSrv *sdk.Server) {
	// memory_read：readOnly
	sdkSrv.AddTool(&sdk.Tool{
		Name:        "memory_read",
		Description: "读取 memory/ 根内文件内容并返回（readOnly）。path 必须是 memory/ 内的相对路径，路径穿越（../、绝对路径、符号链接逃逸）返回错误。",
		InputSchema: inputSchema(map[string]any{
			"path": prop("string", "相对 memory/ 根的文件路径（正斜杠分隔）", nil),
		}, "path"),
		Annotations: readOnlyAnnotations("读取记忆文件"),
	}, withRecovery("memory_read", s.handleRead))

	// memory_write：destructive + idempotent
	sdkSrv.AddTool(&sdk.Tool{
		Name:        "memory_write",
		Description: "写入或新建 memory/ 内文件（destructive, idempotent）。overwrite=false（默认）时目标已存在返回错误；overwrite=true 时原子覆盖。写后立即更新检索索引。",
		InputSchema: inputSchema(map[string]any{
			"path":      prop("string", "相对 memory/ 根的文件路径（正斜杠分隔）", nil),
			"content":   prop("string", "文件正文内容", nil),
			"overwrite": prop("boolean", "是否覆盖已存在文件（默认 false）", false),
		}, "path", "content"),
		Annotations: writeAnnotations("写入记忆文件", true),
	}, withRecovery("memory_write", s.handleWrite))

	// memory_append：destructive（非幂等）
	sdkSrv.AddTool(&sdk.Tool{
		Name:        "memory_append",
		Description: "向 memory/ 内文件末尾追加内容（destructive，非幂等），自动插入时间戳分隔行（<!-- appended <RFC3339> -->）。文件不存在时自动创建（含父目录）。适合 agent 沉淀记忆。",
		InputSchema: inputSchema(map[string]any{
			"path":    prop("string", "相对 memory/ 根的文件路径（正斜杠分隔）", nil),
			"content": prop("string", "要追加的内容", nil),
		}, "path", "content"),
		Annotations: writeAnnotations("追加记忆内容", false),
	}, withRecovery("memory_append", s.handleAppend))

	// memory_search：readOnly
	sdkSrv.AddTool(&sdk.Tool{
		Name:        "memory_search",
		Description: "FTS5 全文检索记忆库（readOnly），返回 文件路径+命中片段，最多 limit 条（默认 20）。query 为要检索的关键词，支持中文（单字切分，与索引侧一致）。",
		InputSchema: inputSchema(map[string]any{
			"query": prop("string", "检索关键词", nil),
			"limit": prop("integer", "最多返回命中条数（默认 20）", 20),
		}, "query"),
		Annotations: readOnlyAnnotations("检索记忆库"),
	}, withRecovery("memory_search", s.handleSearch))

	// memory_list：readOnly
	sdkSrv.AddTool(&sdk.Tool{
		Name:        "memory_list",
		Description: "列出 memory/ 内目录下的文件与子目录（readOnly，不递归）。目录项按名称排序，子目录名以 / 结尾。path 缺省或为 \".\" 时列根目录。",
		InputSchema: inputSchema(map[string]any{
			"path": prop("string", "相对 memory/ 根的目录路径（缺省为根目录）", "."),
		}),
		Annotations: readOnlyAnnotations("列出记忆目录"),
	}, withRecovery("memory_list", s.handleList))

	// memory_status：readOnly
	sdkSrv.AddTool(&sdk.Tool{
		Name:        "memory_status",
		Description: "记忆库统计（readOnly）：文件数、总大小（字节）、最近变更时间。客户端可轮询本工具感知记忆库变更（v1 不实现 MCP notifications）。",
		InputSchema: inputSchema(nil),
		Annotations: readOnlyAnnotations("记忆库统计"),
	}, withRecovery("memory_status", s.handleStatus))
}

// ---- 参数与结果工具函数 ----

// decodeArgs 解析工具参数 JSON 到 out；参数缺失（nil/空）按 {} 处理。
func decodeArgs(name string, raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: invalid arguments: %w", name, err)
	}
	return nil
}

// errResult 构造工具错误响应：IsError=true，错误文本进 Content
// （SDK 语义：工具执行错误不上升为协议错误）。
func errResult(err error) *sdk.CallToolResult {
	res := &sdk.CallToolResult{}
	res.SetError(err)
	return res
}

// textResult 构造成功响应：单段文本 Content。
func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// withRecovery 包装工具 handler：panic 转为工具错误响应，防止单个工具
// 崩溃拖垮整个会话/进程（编码规范 §3.4：库代码禁止裸 panic 逃逸）。
func withRecovery(tool string, h sdk.ToolHandler) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (res *sdk.CallToolResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				res = errResult(fmt.Errorf("%s: internal error: %v", tool, r))
			}
		}()
		return h(ctx, req)
	}
}

// toolError 把底层错误映射为用户可读的工具错误（保留哨兵语义供 errors.Is）。
func (s *Server) toolError(tool string, err error) error {
	switch {
	case errors.Is(err, memory.ErrPathOutsideRoot):
		return fmt.Errorf("%s: path escapes memory root (path traversal rejected): %w", tool, err)
	case errors.Is(err, memory.ErrExists):
		return fmt.Errorf("%s: file already exists and overwrite=false: %w", tool, err)
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s: no such file or directory: %w", tool, err)
	default:
		return fmt.Errorf("%s: %w", tool, err)
	}
}

// boolPtr 返回 bool 指针（SDK ToolAnnotations.DestructiveHint/OpenWorldHint 为 *bool）。
func boolPtr(b bool) *bool { return &b }

// readOnlyAnnotations 标注只读工具：readOnlyHint=true，openWorldHint=false
// （记忆库是闭域，工具只操作 memory/ 内文件）。
func readOnlyAnnotations(title string) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{
		ReadOnlyHint:  true,
		OpenWorldHint: boolPtr(false),
		Title:         title,
	}
}

// writeAnnotations 标注写工具：destructiveHint=true，openWorldHint=false；
// idempotent 按工具实际语义（write 幂等、append 非幂等）。
func writeAnnotations(title string, idempotent bool) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{
		DestructiveHint: boolPtr(true),
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolPtr(false),
		Title:           title,
	}
}

// prop 构造 JSON Schema 属性（type + description + 可选 default）。
func prop(typ, desc string, def any) map[string]any {
	p := map[string]any{"type": typ, "description": desc}
	if def != nil {
		p["default"] = def
	}
	return p
}

// inputSchema 构造工具输入 JSON Schema（2020-12 draft，additionalProperties=false）。
// 无属性时省略 properties 字段（避免 "properties":null 污染客户端 schema）。
// 参数均为字面量，marshal 失败属编程错误，在注册期 panic 暴露（等同启动错误）。
func inputSchema(props map[string]any, required ...string) json.RawMessage {
	m := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}
	if len(props) > 0 {
		m["properties"] = props
	}
	if len(required) > 0 {
		m["required"] = required
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("mcp: build input schema: %v", err))
	}
	return b
}

// ---- 工具 handler ----

func (s *Server) handleRead(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs("memory_read", req.Params.Arguments, &args); err != nil {
		return errResult(err), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errResult(errors.New("memory_read: missing required argument \"path\"")), nil
	}
	data, err := s.store.Read(args.Path)
	if err != nil {
		return errResult(s.toolError("memory_read", err)), nil
	}
	return textResult(string(data)), nil
}

func (s *Server) handleWrite(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var args struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := decodeArgs("memory_write", req.Params.Arguments, &args); err != nil {
		return errResult(err), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errResult(errors.New("memory_write: missing required argument \"path\"")), nil
	}
	if err := s.store.Write(args.Path, []byte(args.Content), args.Overwrite); err != nil {
		return errResult(s.toolError("memory_write", err)), nil
	}
	s.indexFile(args.Path) // §6.2 写入顺序：临时文件 → rename → 索引
	return textResult(fmt.Sprintf("wrote %s (%d bytes)", args.Path, len(args.Content))), nil
}

func (s *Server) handleAppend(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs("memory_append", req.Params.Arguments, &args); err != nil {
		return errResult(err), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return errResult(errors.New("memory_append: missing required argument \"path\"")), nil
	}
	if err := s.store.Append(args.Path, args.Content); err != nil {
		return errResult(s.toolError("memory_append", err)), nil
	}
	s.indexFile(args.Path) // §6.2 写入顺序
	return textResult(fmt.Sprintf("appended %d bytes to %s", len(args.Content), args.Path)), nil
}

func (s *Server) handleSearch(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeArgs("memory_search", req.Params.Arguments, &args); err != nil {
		return errResult(err), nil
	}
	if strings.TrimSpace(args.Query) == "" {
		return errResult(errors.New("memory_search: missing required argument \"query\"")), nil
	}
	results, err := s.ix.Search(args.Query, args.Limit)
	if err != nil {
		return errResult(fmt.Errorf("memory_search: %w", err)), nil
	}
	if len(results) == 0 {
		return textResult("no hits"), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d hit(s):\n", len(results))
	for _, r := range results {
		fmt.Fprintf(&b, "%s: %s\n", r.Path, r.Snippet)
	}
	return textResult(b.String()), nil
}

func (s *Server) handleList(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs("memory_list", req.Params.Arguments, &args); err != nil {
		return errResult(err), nil
	}
	entries, err := s.store.List(args.Path)
	if err != nil {
		return errResult(s.toolError("memory_list", err)), nil
	}
	if len(entries) == 0 {
		return textResult("(empty)"), nil
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name
		if e.IsDir {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return textResult(b.String()), nil
}

func (s *Server) handleStatus(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	var args struct{}
	if err := decodeArgs("memory_status", req.Params.Arguments, &args); err != nil {
		return errResult(err), nil
	}
	st, err := s.store.Status()
	if err != nil {
		return errResult(s.toolError("memory_status", err)), nil
	}
	last := "-"
	if !st.LastModified.IsZero() {
		last = st.LastModified.Format("2006-01-02 15:04:05 MST")
	}
	return textResult(fmt.Sprintf("files: %d\nbytes: %d\nlast_modified: %s",
		st.FileCount, st.TotalBytes, last)), nil
}
