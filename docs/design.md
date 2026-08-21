# ZipperAgentMemory 设计文档 v0.1（草案，待审批）

> 状态：`草案` — 本文件是项目的唯一权威设计来源，批准后任何变更须先改本文档再重新获批。
> 批准人：用户　撰写：协调者

## 1. 需求概述

构建一个 **AI 跨平台永久性记忆工具**：

- 记忆以 **Markdown + CSV/Markdown 表格** 文件、多目录结构持久化在服务器磁盘上；
- 服务器上运行一个 **常驻守护进程** 维护记忆（监听变更、建索引、提供访问接口）；
- Agent 通过 **MCP（stdio）** 访问记忆库，skill 作为无 MCP 场景的补充；
- 记忆迁移支持 **目录整体复制/打包** 与 **git 仓库同步** 两种模式；
- 服务器性能较低，**资源占用是硬约束**。

## 2. 已确认决策（来自需求澄清）

| # | 决策点 | 结论 |
|---|--------|------|
| D1 | 接入方 | 尚未确定，**按通用 MCP 设计**（MCP 优先，skill 补充） |
| D2 | 结构化数据格式 | **CSV / Markdown 表格**，不使用 .xlsx（二进制、不可 diff、解析重） |
| D3 | 迁移方式 | **目录整体复制/打包** + **git 同步**（clone / bundle 单文件）双支持 |
| D4 | 服务器环境 | Linux |
| D5 | 实现语言 | **Go（推荐）**，用户亦熟悉 C#（最终选型见 §3.1） |
| D6 | 记忆内容 | 混合：个人笔记 / 项目知识库 / 结构化数据 / Agent 自动沉淀 |
| D7 | 项目名 | ZipperAgentMemory（进程名 `zipper-agent-memoryd`） |

> 说明：守护进程、记忆库、git 仓库**全部运行在同一台 Linux 服务器上**，客户端仅通过网络接口访问；
> 因此「git autocommit」指守护进程在服务器上对 `memory/` 仓库自动提交，无「服务器 vs 本地」之分。

## 3. 技术选型

### 3.1 语言：Go（待用户最终确认）

| 维度 | Go | C# (.NET 8+) |
|------|----|--------------|
| 部署 | 静态单二进制，零运行时依赖 | 需 .NET 运行时或 NativeAOT 发布 |
| 常驻内存 | 典型 20–50MB | 相似量级，NativeAOT 略高 |
| MCP SDK | `mark3labs/mcp-go` 或官方 `modelcontextprotocol/go-sdk`，生态成熟 | 官方 `modelcontextprotocol/csharp-sdk` 成熟 |
| 文件监听 | `fsnotify` | `FileSystemWatcher`（Linux 走 inotify） |
| SQLite | `modernc.org/sqlite`（纯 Go 无 CGO，交叉编译简单） | `Microsoft.Data.Sqlite` |

**推荐 Go**：低配 Linux 服务器上单二进制部署最省心、无 CGO 交叉编译、内存可控。若用户更倾向 C# 亦可，架构不变。

### 3.2 核心依赖

- `fsnotify` — 目录变更监听（增量索引）
- `modernc.org/sqlite` — SQLite，启用 FTS5 全文检索
- `mark3labs/mcp-go` 或官方 go-sdk — MCP stdio server
- `git` 命令调用（可选 autocommit，不内置 git 库）

### 3.3 资源预算（硬约束）

| 指标 | 目标 | 手段 |
|------|------|------|
| 常驻内存 | **< 60MB** | Go 单进程；索引仅文件路径+元数据+FTS 内容 |
| CPU | 空闲≈0；变更时增量更新 | fsnotify 事件合并（去抖），不做轮询 |
| 磁盘 | 记忆文件 + 索引（估文件体积 10–20%） | SQLite 单文件索引 |
| 明确避免 | 向量库（Qdrant/Chroma）、embedding 模型、Elasticsearch、watchman | 全部不引入 |

## 4. 架构

```
┌─────────────────────┐   stdio    ┌────────────────────────────────┐
│ 任意 MCP 客户端      │ ◄────────► │ zipper-agent-memoryd (Go 单二进制) │
│ Claude/Cursor/DSH…  │            │  ├─ MCP stdio server           │
└─────────────────────┘            │  ├─ fsnotify watcher           │
                                   │  ├─ SQLite FTS5 索引           │
                                   │  └─ git autocommit（可选开关）  │
                                   └──────────────┬─────────────────┘
                                                  │ 读写
                                   ┌──────────────▼─────────────────┐
                                   │ memory/  ← git 仓库             │
                                   │   README.md  index.md          │
                                   │   notes/ projects/ structured/ │
                                   │   agent/ meta/                 │
                                   └────────────────────────────────┘
```

- **单一进程**：watcher、索引、MCP server 合体，避免多进程内存叠加；
- **进程只做编排**：读写本质是文件操作，进程崩溃不丢数据（文件即真源）；
- **索引可重建**：`memory/` 目录丢失索引文件无碍，进程可用 `--rebuild-index` 全量重建。

## 5. 记忆库目录结构（Schema v1）

```
memory/                      ← 整个目录即 git 仓库
├── README.md                ← 记忆库入口说明（人读）
├── index.md                 ← 总索引：目录导航 + 最近更新（可自动维护）
├── notes/                   ← 通用笔记：每主题一个 .md（文件名=主题，蛇形命名）
│   └── go-mcp-development.md
├── projects/                ← 项目知识：每项目一个子目录
│   └── <project>/
│       ├── README.md        ← 项目概况
│       ├── decisions.md     ← 决策记录（ADR 风格）
│       └── <topic>.md
├── structured/              ← 结构化数据（CSV / Markdown 表格）
│   ├── contacts.csv
│   └── tasks.csv
├── agent/                   ← Agent 自动沉淀的记忆
│   └── <agent-id>/          ← 每 Agent 独立目录，避免互相污染
│       └── YYYY-MM.md       ← 按月归档，按需追加
└── meta/                    ← 记忆库元数据（标签表、来源记录、schema 版本）
    └── schema-version       ← 当前目录 schema 版本号
```

**文件约定**：

- 所有记忆文件正文用 Markdown；结构化数据用 CSV 或 Markdown 表格，二者择一，**同库内不混用同一主题的两种格式**；
- 文件首行（CSV 为表头）作为内容摘要来源；
- 文件名即浅层检索键：蛇形命名，禁止空格与中文文件名（内容可以中文）；
- 每个文件前几行可含 YAML front-matter（`tags`、`created`、`source`），供 FTS 与索引使用。

## 6. MCP 接口设计

### 6.1 Tools（全部 `memory_` 前缀，蛇形命名）

| Tool | 参数 | 说明 |
|------|------|------|
| `memory_read` | `path` | 读取文件内容（限制必须在 memory/ 内，防路径穿越） |
| `memory_write` | `path, content, overwrite?` | 写入/新建文件；`overwrite=false` 时存在即报错（默认） |
| `memory_append` | `path, content` | 追加内容（agent 沉淀用，自动加时间戳分隔） |
| `memory_search` | `query, limit?` | FTS5 全文检索，返回 文件路径+命中片段 |
| `memory_list` | `path?` | 列出目录下的文件/子目录 |
| `memory_status` | — | 记忆库统计：文件数、总大小、最近变更 |

### 6.2 安全与并发

- **路径沙箱**：所有 `path` 参数解析后必须落在 memory/ 根内，防穿越；
- **并发写**：进程内互斥锁串行化写操作；文件写入采用「临时文件 + rename」原子替换；
- **读不受锁影响**：只读文件，不阻塞。

### 6.3 事件与通知（v1 可选）

- 不实现 MCP notifications（v1 保持精简）；客户端通过轮询 `memory_status` 感知变更。

## 7. 迁移方案（双模式）

1. **目录复制**：直接 `cp -r memory/` 或打包 `zip/tar`，目标机解压后 `zipper-agent-memoryd --rebuild-index` 即可使用；
2. **git 同步**：
   - 有远程：`git clone` / `push` / `pull`；
   - 无远程：`git bundle create memory.bundle --all` 产出**单文件**，目标机 `git clone memory.bundle`；
3. 迁移后无需人工合并任何状态文件 —— SQLite 索引只是缓存，可随时重建。

## 8. 阶段划分与验收标准

> 每阶段结束：跑通验收用例 → 提交 Git（Conventional Commits）→ 汇报。

### 阶段 0：项目初始化 + 规范
- 交付：Go 模块骨架、目录结构、`docs/standards/go-编码规范.md`、Git 仓库初始化、README
- 验收：`go build ./...` 通过；规范文件存在且覆盖命名/错误处理/并发/提交约定

### 阶段 1：记忆库核心（无进程）
- 交付：`memory/` 目录骨架 + 示例文件；`pkg/memory`：路径沙箱校验、读/写/追加（原子写）
- 验收：CLI 子命令 `zam write/read/append` 可对 memory/ 正确读写；路径穿越被拒绝

### 阶段 2：守护进程（watcher + 索引）
- 交付：`fsnotify` 监听（去抖合并）、SQLite FTS5 索引、`--rebuild-index`、`--serve`
- 验收：修改文件后 ≤2s 索引可见新内容；搜索命中正确；删除文件索引同步移除；`--rebuild-index` 后结果一致；常驻内存 < 60MB（`/usr/bin/time -v` 验证）

### 阶段 3：MCP stdio server
- 交付：实现 §6 全部 6 个 tools；MCP 握手与工具注册
- 验收：用 MCP Inspector 或任意 MCP 客户端连接，6 个工具全部可用；路径穿越返回错误；并发写不丢数据

### 阶段 4：git 集成
- 交付：可选 autocommit（变更去抖后提交，关闭开关默认开启？——**默认关闭，由用户显式开启**）；迁移脚本 `scripts/migrate.sh`（打包/重建索引）
- 验收：autocommit 开启时文件变更自动生成提交；迁移脚本在干净目录还原出可用记忆库

### 阶段 5：测试、文档、交付
- 交付：单元测试（内存/索引/MCP）、集成测试、`docs/memory/` 项目记忆、README 使用文档
- 验收：`go test ./...` 全绿；文档与实现一致；给出资源实测报告

## 9. 风险与开放问题

| # | 风险/问题 | 应对 |
|---|-----------|------|
| R1 | MCP Go SDK 选型（mark3labs vs 官方） | 阶段 3 前做最小握手验证再定，写入阶段 3 任务书 |
| R2 | 多 Agent 并发写同一文件 | 进程级互斥 + 原子写；文档约定 agent 只写自己目录 |
| R3 | Agent 自动沉淀格式失控 | 仅通过 `memory_append` 写入 agent/ 目录，格式由工具强约束 |
| R4 | git 仓库随记忆增长膨胀 | autocommit 默认关闭；定期 `git gc` 提示写入手册 |
| R5 | CSV 与 Markdown 表格混用 | schema 约定同一主题二选一；`memory_status` 可预警 |
| R6 | **basic-memory 为 AGPL-3.0**：只可借鉴设计思想，严禁复制其代码 | 独立实现，代码全部原创；调研报告只做设计参考，不摘录代码 |
| R7 | 索引与文件短暂不一致 | 索引=derived state（eventually consistent），文件=canonical state；不做加锁强一致，可随时 `--rebuild-index` |

## 10. 待用户拍板事项

1. **语言选型**：确认 Go（推荐）还是 C#；
2. **项目名**：沿用目录名 `ZipperAgentMemory`（进程名 `zipper-agent-memoryd`）还是另起；
3. **git autocommit**：默认关闭（推荐）还是默认开启；
4. 本设计文档整体批准或提出修改。
