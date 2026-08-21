package mcp

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/memory"
)

// Version 是 MCP server 实现版本（SDK Implementation.Version 与进程版本号
// cmd/zipper-agent-memoryd 保持同步）。
const Version = "v0.3.0"

// Server 是 ZipperAgentMemory 的 MCP 服务器：把 memory 库 6 个 memory_* 工具
// 注册到官方 go-sdk，支持双传输（design.md §4.1 方案 A）：
//
//   - serve 常驻模式：[Handler] 返回 streamable HTTP handler，由调用方挂载
//     到 /mcp（默认 http://127.0.0.1:8931/mcp）；
//   - stdio 按需模式：[RunStdio] 以 stdin/stdout 运行，供纯 stdio 客户端。
//
// 并发模型：写操作由 Store 内部互斥锁串行化（读不加锁）；索引更新走
// 写后同步（[Server.indexFile]，derived state，失败仅记日志可由 watcher/
// --rebuild-index 恢复）。
type Server struct {
	store *memory.Store
	ix    *index.Index
	sdk   *sdk.Server
}

// New 创建 MCP server 并注册全部 6 个工具（tools.go）。
func New(store *memory.Store, ix *index.Index) *Server {
	s := &Server{store: store, ix: ix}
	s.sdk = sdk.NewServer(&sdk.Implementation{
		Name:    "zipper-agent-memoryd",
		Version: Version,
	}, &sdk.ServerOptions{
		Instructions: "ZipperAgentMemory 记忆库工具。所有 path 参数必须是 memory/ " +
			"根内的相对路径（正斜杠分隔）；路径穿越会被拒绝。memory_write 默认" +
			"不覆盖已存在文件（overwrite=false）。memory_search 走 FTS5 全文检索" +
			"（支持中文关键词）。",
	})
	s.registerTools(s.sdk)
	return s
}

// Handler 返回 streamable HTTP transport 的 http.Handler（serve 模式挂载到
// /mcp）。响应格式配置为 application/json（JSONResponse），便于命令行客户端
// 与 MCP Inspector 等直接调试；对单消息 POST 返回 JSON，多消息/流式场景
// 仍按协议处理。
func (s *Server) Handler() http.Handler {
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return s.sdk },
		&sdk.StreamableHTTPOptions{JSONResponse: true},
	)
}

// RunStdio 以 stdio transport 运行（按需拉起，供纯 stdio 客户端）。
// 阻塞至 ctx 取消或客户端关闭连接（stdin EOF）。
func (s *Server) RunStdio(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdk.StdioTransport{})
}

// indexFile 在写操作（memory_write / memory_append）成功后同步更新索引，
// 遵循设计文档 §6.2 的写入顺序：临时文件 → rename → 索引。
//
// 索引是 derived state：更新失败只记日志，不把写操作回滚为失败——文件是
// canonical state，fsnotify watcher 与 --rebuild-index 都能恢复一致性（§9 R7）。
// 仅索引 ShouldIndex 认可的可索引文件（跳过隐藏/.tmp-*/非文本扩展名）。
func (s *Server) indexFile(rel string) {
	rel = filepath.ToSlash(rel)
	if !index.ShouldIndex(rel, false) {
		return
	}
	abs := filepath.Join(s.store.Root(), filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if err != nil {
		log.Printf("mcp: read after write %s: %v", rel, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		log.Printf("mcp: stat after write %s: %v", rel, err)
		return
	}
	m, body := index.ParseFrontMatter(data)
	m.MTime = info.ModTime()
	m.Size = info.Size()
	if err := s.ix.Upsert(rel, m, []byte(body)); err != nil {
		log.Printf("mcp: index upsert %s: %v", rel, err)
	}
}
