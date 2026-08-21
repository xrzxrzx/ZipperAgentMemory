# 任务书（草稿）：阶段 3 —— MCP server

> 状态：**草稿，待设计文档批准后生效**。⚠️ 其中 MCP 传输形态依赖用户拍板（design.md §4.1：A/B/C），
> 本任务书按**推荐方案 A（serve 常驻 HTTP + stdio 按需双模式）**编写；若用户选 B/C 需小幅调整传输层。

## 任务书：阶段 3 MCP server

- **派发者**：协调者
- **依据**：`docs/design.md` §4.1（传输形态）、§6（接口设计）；`docs/research/mcp-go-sdk-选型.md`；`docs/standards/go-编码规范.md`
- **目标**：实现 MCP server，暴露 6 个工具供 agent 读写检索记忆库
- **前置**：阶段 1（`pkg/memory`）、阶段 2（`pkg/index`）已交付
- **范围**：
  - 依赖：**官方 `modelcontextprotocol/go-sdk`，锁定 v1.7.x**（Tier 1；mark3labs 备选）
  - `pkg/mcp`：
    - 注册 6 个 tools（见 design.md §6.1）：`memory_read`（readOnly）、`memory_write`（destructive, idempotent）、`memory_append`（destructive）、`memory_search`（readOnly）、`memory_list`（readOnly）、`memory_status`（readOnly）
    - 每个 tool 带行为标注；参数经路径沙箱校验（复用 `pkg/memory`），穿越返回结构化错误
    - 写操作串行化：进程内互斥 + 原子写（复用阶段 1）
    - handler 外层统一 defer/recover（panic → MCP 错误响应，借鉴调研建议）
  - `cmd/zipper-agent-memoryd` 扩展：
    - `serve`：HTTP transport（streamable HTTP，监听 `127.0.0.1:PORT`）+ watcher/索引（常驻）
    - `stdio`：stdio transport（按需拉起，供纯 stdio 客户端）
  - **不做**：MCP prompts/notifications（v1 后）、客户端 OAuth、任务型工具
- **验收标准**：
  1. MCP Inspector 或任意 MCP 客户端连接成功，6 个 tools 全部可用（握手 + 调用）；
  2. `memory_read/write/append/search/list/status` 功能正确（对接真实 memory/）；
  3. 路径穿越返回错误（`../`、绝对路径、符号链接逃逸）；
  4. 并发写不丢数据（并发 append/write 测试）；
  5. `go test ./...` 全绿、`go vet ./...` 零告警；
  6. （serve 模式）常驻内存仍 < 60MB 红线。
- **交付物**：`pkg/mcp/*`（含测试）、`cmd/zipper-agent-memoryd` 扩展、MCP Inspector 验证记录
- **约束**：官方 go-sdk 锁 v1.7.x；**不得**引入 mark3labs（除非官方版遇到不可解问题，需先报协调者）；遵守编码规范；提交 `feat(mcp): ...` 等 Conventional Commits
- **DoD**：6 项验收通过且证据留档（`docs/验收/阶段3.md`）
