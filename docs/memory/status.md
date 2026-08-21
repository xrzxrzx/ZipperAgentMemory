# 项目状态快照

> 更新规则：每次阶段结束/重要决策后更新本文件，保持为「当前事实」，不保留历史（历史见 git）。

- 更新日期：2026-08-21
- 当前阶段：**设计阶段（阶段 0 前）** —— 设计文档待用户批准，编码未开工

## 项目定义

AI 跨平台永久性记忆工具：Linux 低资源服务器上 Go 常驻进程，维护 Markdown/CSV 多目录记忆库，经 MCP stdio 供 agent 读写检索，支持目录复制与 git 双模式迁移。

## 基础设施（本轮新增）

- GitHub 远程：`git@github.com:xrzxrzx/ZipperAgentMemory.git`（已建好，本地已 init + 首次 commit `85f3c7c`）
  - ⚠️ 推送受阻：SSH key 被添加为仓库 Deploy Key（只读），需用户勾选 Allow write access 或改账号级 Authentication Key
  - 网络：GitHub 22 端口被墙，已配置 SSH over 443（`~/.ssh/config` 追加 github.com → ssh.github.com:443）
- 部署服务器：`8.141.89.50`（SSH alias `minichat-server`，密钥认证可用，无需密码）
  - 环境：阿里云 Linux 8（al8），2 核 / 1870MB 内存 / 40G 磁盘（可用 32G）
  - 已装：sqlite3；**未装：Go、git**（部署阶段再装）
  - 用途（用户确认）：**部署到这台服务器**

## 已确认决策（用户拍板）

| 编号 | 决策 | 状态 |
|------|------|------|
| D1 | 接入方未定，按通用 MCP 设计（MCP 优先，skill 补充） | ✅ 已确认 |
| D2 | 结构化数据用 CSV/Markdown 表格，不用 .xlsx | ✅ 已确认 |
| D3 | 迁移：目录复制/打包 + git 同步 双支持 | ✅ 已确认 |
| D4 | 服务器 Linux | ✅ 已确认 |
| D5 | 语言 **Go** | ✅ 已确认 |
| D6 | 记忆内容混合（笔记/项目库/结构化/agent 沉淀） | ✅ 已确认 |
| D7 | 项目名 ZipperAgentMemory（进程 zipper-agent-memoryd） | ✅ 已确认 |
| D8 | git autocommit 默认关闭（开关可切换） | ⏳ 已推荐，待用户最终点头 |
| D9 | 设计文档 v0.1 整体批准 | ⏳ 用户表示「先等一下」，待继续讨论 |

## 调研结论（本轮完成，报告在 docs/research/）

1. **basic-memory 调研**：架构同构（文件=canonical、SQLite=derived 索引）；Python 栈偏重（≥3.12、~40 依赖）；AGPL-3.0 不可抄码；默认开向量搜索（低配可关）；23 工具+5 prompts 均带行为标注。借鉴：canonical/derived 分层、FTS5、stdio 按需启动；回避：Python 栈、向量、图谱、云蔓延。
2. **MCP Go SDK 选型**：**选官方 `modelcontextprotocol/go-sdk` v1.7.x**（Tier 1、稳定、规范跟进最快）；mark3labs/mcp-go 备选（生态大但刚进 beta）。已固化进设计文档 §3.2 与 R1。

## 设计文档

- 主文档：`docs/design.md`（v0.1 草案 + 调研增量：§3.2 依赖选型、§4.1 MCP 传输形态三方案、§6.1 工具行为标注、R1 已解决、R6 AGPL、R7 derived-state、写链路稳定性要求）
- 编码规范：`docs/standards/go-编码规范.md`（已就绪）
- 调研报告：`docs/research/basic-memory-调研.md`、`docs/research/mcp-go-sdk-选型.md`

## 待用户拍板事项（更新）

1. ~~语言~~ ✅ Go；2. ~~项目名~~ ✅ ZipperAgentMemory；3. git autocommit 默认开关；4. **MCP 传输形态（新增）**：A 单常驻+HTTP（推荐）/ B 常驻+stdio 转发 / C 纯 stdio；5. 设计整体批准

## 资源红线（硬约束）

常驻内存 <60MB；空闲 CPU≈0（事件驱动）；禁止引入向量库/embedding/ES。

## 下一步

1. 用户修复 GitHub key 写入权限 → 推送首次 commit
2. 向用户汇报调研结论 + 待讨论项 → 设计文档 v0.2 → 批准 → 开工阶段 0
3. 部署规划：服务器装 Go（1.25+）与 git（部署阶段执行）
