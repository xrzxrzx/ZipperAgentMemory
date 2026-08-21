# integration — 端到端验证（阶段 3 MCP server）

本目录是「真实进程 + 双传输」的端到端验证，对应
`docs/验收/阶段3.md` 验收标准 6（stdio 模式可用）与 serve（streamable HTTP）
模式的实际验证。与 `pkg/mcp` 的内存传输单元测试互补：这里构建真实二进制
`zipper-agent-memoryd`，以子进程拉起后走完整 MCP 协议。

## 运行

```powershell
# Go 工具链需在 PATH
$env:Path = "C:\Users\xrzxr\sdk\go1.26.7\bin;" + $env:Path
cd D:\Works\ZipperAgentMemory
go test -tags e2e ./integration/ -v
```

## 覆盖

| 测试 | 模式 | 覆盖点 |
|------|------|--------|
| `TestE2EStdio` | stdio | initialize 握手 + notifications/initialized + tools/list（6 工具齐全）+ 6 个工具功能 + 路径穿越拒绝（IsError 工具错误） |
| `TestE2EHTTP` | serve（streamable HTTP） | initialize 握手（含 Mcp-Session-Id 会话维持）+ tools/list + 6 个工具功能 + 路径穿越拒绝 |

## 设计说明

- 测试文件带 `//go:build e2e` 标签：`go test ./...`（CI 常规）不包含，
  手动可跑（编码规范 §8.3：集成测试放 integration/，CI 之外手动）。
- stdio 走原始 JSON-RPC（newline-delimited），HTTP 走 `application/json`
  响应（服务端配置 `JSONResponse: true`，见 `pkg/mcp/server.go`）。
- 记忆样本含中文内容，验证 `memory_search` 中文关键词命中。
- 端口用 `freePort()` 挑空闲端口，与子进程绑定之间理论上存在极小竞争；
  失败时把 serve 的 stderr 打出来定位。
