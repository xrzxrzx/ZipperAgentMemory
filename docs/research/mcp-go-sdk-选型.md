# Go MCP Server SDK 选型调研报告（mark3labs/mcp-go vs modelcontextprotocol/go-sdk）

> 调研日期：2026-08-21
> 调研对象：mark3labs/mcp-go、modelcontextprotocol/go-sdk（官方）、metoro-io/mcp-golang 等
> 调研方式：一手来源——GitHub 仓库 README/源码/go.mod/Release Notes/Issues（GitHub REST API）、pkg.go.dev 文档、官方文档 modelcontextprotocol.io（SDK 列表、SDK 分级体系）、官方 conformance 审计 issue、go-sdk 官方 docs 目录

## 来源列表（一手）

- mark3labs/mcp-go 仓库与 Releases API：<https://github.com/mark3labs/mcp-go>；<https://api.github.com/repos/mark3labs/mcp-go>
- mark3labs 文档站：<https://mcp-go.dev/getting-started/>；pkg.go.dev：<https://pkg.go.dev/github.com/mark3labs/mcp-go>
- modelcontextprotocol/go-sdk 仓库与 Releases API：<https://github.com/modelcontextprotocol/go-sdk>；<https://api.github.com/repos/modelcontextprotocol/go-sdk>
- 官方 SDK 列表页（含 Tier 分级）：<https://modelcontextprotocol.io/docs/2026-07-28/sdk>；SDK 分级体系说明：<https://modelcontextprotocol.io/community/sdk-tiers>
- Go SDK Tier 1 官方审计（issue #2279）：<https://github.com/modelcontextprotocol/modelcontextprotocol/issues/2279>
- go-sdk 文档（server/client/protocol/rough_edges）：<https://github.com/modelcontextprotocol/go-sdk/tree/main/docs>
- go-sdk v1.7.0 Release Notes：<https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0>
- mcp-go v1.0.0-beta.1 Release：<https://github.com/mark3labs/mcp-go/releases/tag/v1.0.0-beta.1>；2026-07-28 规范支持 PR #951：<https://github.com/mark3labs/mcp-go/pull/951>
- 真实项目选型 ADR（intervals-icu-mcp，2026-04）：<https://github.com/maroffo/intervals-icu-mcp/blob/main/docs/adr/0001-sdk-mark3labs.md>
- metoro-io/mcp-golang：<https://github.com/metoro-io/mcp-golang>
- 2026-07-28 规范变更（stateless 化）：<https://modelcontextprotocol.io/specification/2026-07-28/changelog>

---

## 1. 背景

我们要在**低资源 Linux 服务器**上用 Go 写一个 **MCP stdio server**，提供 read / write / append / search / list / status 六类文件操作工具，且要求**长期维护**。候选 SDK 有两个：

1. **mark3labs/mcp-go** —— 社区 SDK，生态最大、教程最多；
2. **modelcontextprotocol/go-sdk** —— MCP 官方 Go SDK，与 Google 协作维护，2026-02 起被官方评为 **Tier 1**。

两者对本场景（stdio + 文件读写工具）都**功能完全够用**，差距主要体现在：**维护背书、版本稳定性、协议符合度验证**三个维度，以及「上手易用性/生态」的权衡。

---

## 2. mark3labs/mcp-go 调研

### 2.1 仓库活跃度与维护状态

- **Stars 9,018 / Forks 874 / Open Issues 32**，MIT 许可，创建于 2024-11-27，最近 push 2026-08-12。[GitHub API](https://api.github.com/repos/mark3labs/mcp-go)
- **发布极高频**：自 2024-12-12（v0.6.0）至今已发布 88+ 个版本（GitHub Releases API 每页 100 条内 88 条非预发布）。近三个月：v0.55.1（06-25）→ v0.56.0（07-09）→ v0.57.0（07-23）→ v0.58.0（08-11）→ **v1.0.0-beta.1（2026-08-12）**。[Releases](https://github.com/mark3labs/mcp-go/releases)
- 核心维护者 ezynda3（Ed Zynda），社区贡献者众多；README 声明项目随 MCP 规范一起快速演进（*"under active development, as is the MCP specification itself"*）。[README](https://github.com/mark3labs/mcp-go)
- **Go 版本要求：go 1.25.5**（go.mod）。[go.mod](https://raw.githubusercontent.com/mark3labs/mcp-go/main/go.mod)

### 2.2 功能覆盖

- **传输层**：stdio、SSE、streamable-HTTP 全部支持；`StreamableHTTPServer` 是标准 `http.Handler`，可嵌入任意 net/http 路由，也可用传输无关的 `Handle` 入口适配 fasthttp/fiber。[README](https://github.com/mark3labs/mcp-go)
- **Tools**：声明式 `mcp.NewTool(...)` + `s.AddTool(...)`，含类型安全参数访问、typed tools（泛型强类型 handler）、struct 输入输出 schema、工具注解。[README](https://github.com/mark3labs/mcp-go)
- **Resources**：静态资源 + 动态 URI 模板资源；**Prompts**：完整支持（含嵌入资源、多消息）。[README](https://github.com/mark3labs/mcp-go)
- **进度通知**：`mcp.NewProgressNotification(token, progress, total, message)` 与 `ProgressNotificationParams` 存在于当前版本 mcp 包。[pkg.go.dev](https://pkg.go.dev/github.com/mark3labs/mcp-go/mcp)
- **Streaming**：SSE / streamable HTTP 响应流（含 2026-07-28 规范下的 stateless 流式响应）；长任务可用**任务型工具**（`AddTaskTool` + `WithTaskCapabilities`，异步执行 + 客户端轮询，支持并发上限）替代流式结果。[README](https://github.com/mark3labs/mcp-go)
- **服务端管理能力（本库一大特色）**：会话管理（per-session tools、tool filtering）、请求 Hooks、工具/提示词 handler 中间件、`WithRecovery` panic 恢复、completions（prompt/resource 参数自动补全）、采样/elicitation、OAuth Protected Resource Metadata、CORS、DNS rebinding 防护；另附 `mcptest` 测试辅助包与 otel/tracing 扩展。[README](https://github.com/mark3labs/mcp-go)
- **规范版本**：支持 **2026-07-28**（v1.0.0-beta.1 起，PR #951），并向后兼容 2025-11-25、2025-06-18、2025-03-26、2024-11-05。[README](https://github.com/mark3labs/mcp-go)、[PR #951](https://github.com/mark3labs/mcp-go/pull/951)

### 2.3 API 稳定性与已知问题/坑

- **版本号刚从 0.x 跨入 1.0.0-beta.1（2026-08-12）**，尚无正式 1.0。0.x 时代**跨小版本存在破坏性变更**（社区大量 dependabot 升级 PR 从 0.18 直接跳 0.37 等，见 [apecloud PR](https://github.com/apecloud/kb-cloud-mcp-server/issues/26)），升级需关注 changelog。
- **曾有规范跟进滞后**：smart-mcp-proxy/mcpproxy-go 曾以 issue 跟踪"升级 2026-07-28 规范被 mcp-go 阻塞"（[#532](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/532)），v1.0.0-beta.1（PR #951）才补齐；官方 go-sdk 于 2026-07-28 即发布 v1.7.0 支持。
- **历史 bug 均已闭环**：issue #881「SSE/stdio 传输 goroutine 缺 panic 恢复」（2026-05，已关闭，现库内已有 stdio_panic_test.go / sse_panic_test.go 且提供 `WithRecovery`）；PR #642「notification 破坏 client tool call」（2025-11，已合入）。[#881](https://github.com/mark3labs/mcp-go/issues/881)、[#642](https://github.com/mark3labs/mcp-go/pull/642)
- **测试体系**：自带 wire 级 conformance 套件（txtar 归档，仿官方 SDK 做法）、e2e 兼容矩阵测试、`go test -race` 与 golangci-lint 全绿（PR #951 自述）。[PR #951](https://github.com/mark3labs/mcp-go/pull/951)

### 2.4 社区使用情况（部分知名使用者）

coder/coder、stacklok/toolhive、shyndman/github-mcp-server、smart-mcp-proxy/mcpproxy-go、naotaka3/secure-shell-server、tempo1/tempo、apecloud/kb-cloud-mcp-server、aliwatters/rod-mcp 等（来自 dependabot/依赖追踪与代码库检索）。[示例](https://github.com/stacklok/toolhive/blob/main/pkg/vmcp/server/server.go)、[示例](https://github.com/coder/coder/issues/18031)

---

## 3. 官方 go-sdk（modelcontextprotocol/go-sdk）调研

### 3.1 仓库活跃度与维护状态

- **Stars 5,003 / Forks 518 / Open Issues 88**，仓库描述为 *"The official Go SDK for Model Context Protocol servers and clients. Maintained in collaboration with Google."*，创建于 2025-04-23，最近 push 2026-08-21。[GitHub API](https://api.github.com/repos/modelcontextprotocol/go-sdk)
- **已稳定在 1.x**：共 29 个 release；v0.2.0（2025-07-11）→ **v1.0.0（2025-09-30）** 仅约 3 个月即达 1.0；最新 **v1.7.0（2026-07-28）**，随后 1.7.0-pre.x 曾由 **GitHub MCP server 生产使用（服务 50 万+ 用户）** 验证。[Releases](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)、[GitHub changelog](https://github.blog/changelog/2026-07-23-github-mcp-server-supports-the-next-mcp-specification/)
- **官方 Tier 1 认证（2026-02-27 审计，issue #2279）**：Server conformance 100%（30/30）、Client 100%（date-versioned 20/20）、48/48 特性均有文档+示例、issue 分流率 97.6%、0 个未闭环 P0。官方 SDK 列表页将 Go SDK 标为 **Tier 1**。[#2279](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/2279)、[SDK 列表](https://modelcontextprotocol.io/docs/2026-07-28/sdk)、[Tier 体系](https://modelcontextprotocol.io/community/sdk-tiers)
- **Go 版本要求：go 1.25.0**（go.mod）。[go.mod](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/go.mod)
- 许可：Apache 2.0（新贡献）+ MIT（存量代码）。[README](https://github.com/modelcontextprotocol/go-sdk)

### 3.2 功能覆盖

- **包结构**：`mcp`（server/client 主 API）、`jsonrpc`（自定义传输）、`auth`/`oauthex`（OAuth）；服务端示例 `mcp.NewServer + mcp.AddTool + server.Run(ctx, &mcp.StdioTransport{})` 即可跑通 stdio。[README](https://github.com/modelcontextprotocol/go-sdk)
- **Tools**：`mcp.AddTool(server, &mcp.Tool{...}, typedHandler)` 泛型强类型 handler（输入/输出 Go struct + jsonschema 注解），工具名/输入输出 schema 校验（SEP-2106）。[README](https://github.com/modelcontextprotocol/go-sdk)
- **Resources / Prompts / Completions / Elicitation**：全部实现并有文档示例（审计表 48/48）。[#2279](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/2279)
- **进度通知**：`ProgressNotificationParams` 存在（rough_edges 中提及命名可再优化，功能已实现并有示例）。[rough_edges](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/rough_edges.md)
- **Streaming**：SSE、streamable HTTP（含 `StreamableHTTPOptions.Stateless` 模式）；2026-07-28 规范下新增 `subscriptions/listen` 长连接多路复用变更通知流；长结果支持进度通知而非流式工具结果。[v1.7.0 notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
- **2026-07-28 规范支持完整（v1.7.0）**：stateless 协议（`server/discover` 取代 initialize、按请求 `_meta`）、MRTR 多轮请求（SEP-2322）、可缓存列表结果（SEP-2549）、HTTP 头标准化（SEP-2243）；**自动版本协商**（2026-07-28 ↔ 2025-11-25 及更早），存量客户端/服务端不受影响。[v1.7.0 notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
- **兼容策略成熟**：提供 7 个 `MCPGODEBUG` 兼容旗标恢复旧行为（v1.9.0 移除），规范演进不靠破坏性变更硬切。[v1.7.0 notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)

### 3.3 API 稳定性、production-ready 标注与成熟度差距

- **明确 production-ready**：官方 SDK 列表 Tier 1 + 1.x semver 稳定发布 + 官方 conformance 测试 100% + GitHub 生产使用背书，且 README 明确版本兼容表。[SDK 列表](https://modelcontextprotocol.io/docs/2026-07-28/sdk)、[README](https://github.com/modelcontextprotocol/go-sdk)
- 已知"粗糙边"已文档化（docs/rough_edges.md）：均为命名/字段细节（如 `EventStore.Open` 多余、`ProgressNotificationParams` 命名、`ToolAnnotations` 建议 `*bool`），留待 **v2** 处理，不破坏当前 API。[rough_edges](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/rough_edges.md)
- **与 mark3labs 的成熟度差距（2026-08 时点）**：
  - 官方：版本稳定（1.7.0）、Tier 1 符合度审计、规范跟进零滞后（2026-07-28 发布当天即出 v1.7.0）；
  - mark3labs：生态/教程/示例数量更多、服务端高层设施（per-session tools、hooks、中间件、WithRecovery）更丰富，但版本刚进 1.0.0-beta.1，0.x 时代有破坏性变更与一次规范滞后记录。
- 官方 SDK 官方致谢中明确承认 mcp-go / mcp-golang / go-mcp 为受启发的**可行替代**。[README](https://github.com/modelcontextprotocol/go-sdk)

---

## 4. 其他 Go MCP 库简述

- **metoro-io/mcp-golang**：反射式工具注册、代码极简（"few lines of go code"），1225 stars；最近发布 **v0.16.1（2026-02-25）**，距今约 6 个月，**活跃度明显下降**，仍停留 0.x。[仓库](https://github.com/metoro-io/mcp-golang)、[Releases](https://api.github.com/repos/metoro-io/mcp-golang/releases)
- **ThinkInAIXYZ/go-mcp**：官方 README 致谢中提到的另一社区库，生态规模更小，不建议优先考虑。[README](https://github.com/modelcontextprotocol/go-sdk)
- 结论：以上两者在维护活跃度、规范跟进、社区体量上均不如前两个候选，**不建议选用**。

---

## 5. 对比表

| 维度 | mark3labs/mcp-go | modelcontextprotocol/go-sdk（官方） |
|---|---|---|
| **维护状态** | 极活跃：9,018★、88+ releases、月度多版本；核心维护者 ezynda3 + 社区 | 官方 + Google 协作：5,003★、稳定 1.x；**Tier 1 官方认证**；GitHub MCP 生产使用（50万+ 用户） |
| **功能覆盖** | stdio/SSE/streamable-HTTP、tools（typed/任务型）、resources、prompts、completions、进度通知、sessions/hooks/中间件/恢复、OAuth 元数据、采样/elicitation；2026-07-28 规范已支持 | stdio/SSE/streamable-HTTP、tools（泛型 typed）、resources、prompts、completions、elicitation、进度通知、MRTR、stateless 协议、subscriptions/listen；2026-07-28 规范完整支持 |
| **API 稳定性** | 刚进 **1.0.0-beta.1**；0.x 时代有破坏性变更，升级需看 changelog | **正式 1.x（v1.7.0）**，semver + `MCPGODEBUG` 兼容旗标；破坏性变更只留 v2 |
| **协议符合度** | 自带 txtar conformance 套件 + e2e 矩阵（自建，未挂官方 corpus） | **官方 conformance 100%**（server 30/30、client 20/20），Tier 1 审计 |
| **文档质量** | README 极详尽 + mcp-go.dev + 大量示例与第三方教程 | 官方 docs/（server/client/protocol/quick_start/troubleshooting），48/48 特性带示例；文档自动生成，偏简洁 |
| **学习成本** | 低：`NewMCPServer + AddTool + ServeStdio` 声明式，上手最快 | 低-中：`NewServer + AddTool(typed) + Run(StdioTransport)`，泛型 handler 略需理解 jsonschema 注解 |
| **推荐结论** | 备选：要最大生态/最全教程/服务端高层设施时选它 | **首选：长期维护项目选官方**（见第 6 节） |

---

## 6. 推荐结论

### 推荐：modelcontextprotocol/go-sdk（官方 SDK）

理由（对应我们的场景：stdio、文件读写工具、低资源、长期维护）：

1. **长期维护的最强背书**：官方仓库 + Google 协作 + **Tier 1 官方认证**（100% conformance、0 未闭环 P0、48/48 特性文档化），MCP 协议由官方组织主导，SDK 不会因个人维护者精力变化而停摆。[SDK 列表](https://modelcontextprotocol.io/docs/2026-07-28/sdk)、[#2279](https://github.com/modelcontextprotocol/modelcontextprotocol/issues/2279)
2. **版本稳定性**：已是 v1.7.0 稳定版（semver + `MCPGODEBUG` 兼容旗标），而 mcp-go 2026-08-12 才进 1.0.0-beta.1、0.x 时代有跨版本破坏性变更记录。长期维护最怕 API 漂移。[v1.7.0 notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
3. **规范跟进零滞后 + 自动版本协商**：2026-07-28 规范发布当天官方即出 v1.7.0，且新老规范自动协商、存量不受影响；mcp-go 曾出现一次规范滞后（mcpproxy-go #532 被阻塞）。[v1.7.0 notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
4. **本场景功能全覆盖且更贴协议**：`mcp.StdioTransport` + `mcp.AddTool`（泛型 typed handler 自带输入校验，正好适合 6 个文件工具的统一入参模型）+ resources/prompts/进度通知齐备；stdio 下 stateless 化完全无感。[README](https://github.com/modelcontextprotocol/go-sdk)
5. **低资源友好**：纯标准库实现为主、静态二进制、依赖树小（jwt/oauth2/segmentio-encoding 仅在用到 OAuth 时才有意义）；对内存/CPU 敏感的 stdio 单进程场景无压力。Go 要求 1.25.0，低资源 Linux 上装单文件工具链即可。[go.mod](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/go.mod)
6. **生产实证**：v1.7.0-pre.3 被 GitHub MCP server 用于 50 万+ 用户，质量已在大规模生产环境验证。[GitHub changelog](https://github.blog/changelog/2026-07-23-github-mcp-server-supports-the-next-mcp-specification/)

### 若选官方，它当前缺什么（以及怎么补）

| 缺口 | 影响评估（对本项目） | 补齐方式 |
|---|---|---|
| **任务型异步工具**（长任务轮询模式）：mcp-go 有 `AddTaskTool`/`WithTaskCapabilities`；官方 Tasks（SEP-1686）仍为实验性、未实现 | 我们的 6 个文件工具均为同步小操作，**无影响** | 若未来要长任务：自研 polling 工具（`tasks/xxx` 自定义方法官方已支持注册 custom JSON-RPC methods），或等官方 SEP-1686 落地 |
| **服务端高层设施**：per-session tools、tool filtering、request hooks、handler 中间件、`WithRecovery` panic 恢复 | stdio 单客户端场景**影响很小**；panic 恢复属工程必修 | 在 handler 外层自写 `defer/recover` 包装（官方支持自定义方法与 middleware 模式）；stdio 进程级崩溃可交由 systemd 拉起兜底 |
| **客户端侧 OAuth** 仍实验性（官方 ROADMAP 项） | 我们是 **server** 且走 stdio，**无影响** | 不需要 |
| **生态教程/中文资料数量**少于 mcp-go | 官方 48/48 特性文档带示例，官方文档 + 本报告足够起步 | 需要时参考 mcp-go 的示例思路（协议边界小，思路可平移） |
| roots/sampling/logging 在 2026-07-28 规范中被废弃（SEP-2577） | 我们只用 tools/resources/progress，**无影响** | 不启用即可 |

### 备选路径与风险对冲

- 若团队更看重「上手最快、示例最多、社区热度」，**mark3labs/mcp-go 也是完全合理的选择**——两者对本场景都绰绰有余；真实项目 ADR 亦佐证（intervals-icu-mcp 2026-04 选 mark3labs 的理由是当时官方 API 未定型，其"官方到 1.0 后重审"的再评估条件如今已满足）。[ADR](https://github.com/maroffo/intervals-icu-mcp/blob/main/docs/adr/0001-sdk-mark3labs.md)
- 风险对冲：我们的使用面窄（server + AddTool + 6 个工具 + 可选 resources/progress），**协议边界小，两库互迁成本约 1 天**（ADR 同口径）。建议：**默认官方 go-sdk，锁 go.mod 版本（如 v1.7.x）+ 关键路径写 wire 级测试**；一旦官方 SDK 演进过快，可低成本切回 mark3labs。
