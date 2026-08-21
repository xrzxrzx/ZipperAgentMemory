// Package mcp 实现 ZipperAgentMemory 的 MCP 服务器（docs/design.md §6）。
//
// 本包是 MCP 传输层与 6 个 memory_* 工具的唯一实现方，SDK 依赖
// 官方 modelcontextprotocol/go-sdk v1.7.0（docs/research/mcp-go-sdk-选型.md，
// Tier 1 认证），不暴露 SDK 细节给调用方（cmd）。
//
// 覆盖内容：
//   - 6 个工具：memory_read / memory_write / memory_append / memory_search /
//     memory_list / memory_status（design.md §6.1），工具行为标注
//     （readOnly / destructive / idempotent）以 SDK ToolAnnotations 表达，
//     并在工具描述中注明；
//   - 双传输：streamable HTTP（serve 常驻模式，[Server.Handler] 挂载 /mcp）
//     与 stdio（按需拉起模式，[Server.RunStdio]）；
//   - 安全：所有 path 参数经 pkg/memory 沙箱校验（含符号链接/junction 组件级
//     解析），路径穿越返回结构化 MCP 工具错误（IsError=true，LLM 可见可自纠正）；
//   - 并发：写操作复用 Store 互斥锁串行化 + 「临时文件 → rename」原子写，
//     成功后同步更新索引（设计文档 §6.2 写入顺序）；
//   - 健壮性：handler 外层统一 recover，panic 转为 MCP 工具错误响应，不拖垮会话。
//
// 明确不做（v1 范围外）：prompts / notifications / 客户端 OAuth / 任务型工具。
package mcp
