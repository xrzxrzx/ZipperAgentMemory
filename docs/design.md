# ZipperAgentMemory 设计文档 v1.0（已批准）

> 状态：`已批准`（2026-08-21，用户全批准）— 本文档是项目的唯一权威设计来源，变更须先改本文档并重新获批。
> 批准人：用户　撰写：协调者　版本历史：v0.1 草案 → v1.0 批准（含调研增量）→ 2026-10-05 追加 §11「记忆阅读页」与 §10 决策 8（已获用户批准）

## 1. 需求概述

构建一个 **AI 跨平台永久性记忆工具**：

- 记忆以 **Markdown + CSV/Markdown 表格** 文件、多目录结构持久化在服务器磁盘上；
- 服务器上运行一个 **常驻守护进程** 维护记忆（监听变更、建索引、提供访问接口）；
- Agent 通过 **MCP（serve 常驻 HTTP + stdio 双模式）** 访问记忆库，skill 作为无 MCP 场景的补充；
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

| 依赖 | 选型 | 说明 |
|------|------|------|
| MCP SDK | **官方 `modelcontextprotocol/go-sdk`**（锁定 v1.7.x） | Tier 1 认证、稳定版；见 `docs/research/mcp-go-sdk-选型.md` |
| 文件监听 | `fsnotify` | 目录变更监听（增量索引） |
| SQLite | `modernc.org/sqlite` | 纯 Go 无 CGO，启用 FTS5 全文检索 + WAL 模式 |
| git | 命令行调用 | 可选 autocommit，不内置 git 库 |

> SDK 选型依据（调研结论摘要）：官方 go-sdk 为 Tier 1、v1.7.0 稳定版、2026-07-28 规范发布当天跟进；mark3labs/mcp-go 生态最大但刚进 v1.0.0-beta.1、曾有规范跟进滞后。本项目 6 个同步文件工具，官方版缺失的任务型工具/会话设施均无影响；关键路径写测试对冲风险。
> 工程约束：**Go ≥ 1.25.0**（官方 go-sdk v1.7.x 的 go.mod 要求）；锁 v1.7.x 版本，升级走独立提交。

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
- **索引可重建**：`memory/` 目录丢失索引文件无碍，进程可用 `rebuild-index` 子命令全量重建。

### 4.1 MCP 传输形态（已确认：方案 A）

> 已确认（2026-08-21）：**方案 A —— 单常驻进程 + streamable HTTP**，二进制保留 `stdio` 模式。
> 背景：basic-memory 走 stdio 按需拉起（客户端 spawn 进程、会话结束退出），**无常驻进程**；
> 而本设计（你的需求）要求**常驻进程维护记忆**。

| 方案 | 形态 | 优点 | 缺点 |
|------|------|------|------|
| **A. 单常驻进程 + streamable HTTP**（✅ 已确认） | daemon 常驻，监听 `127.0.0.1:PORT`，客户端配置 MCP 指向 `http://127.0.0.1:PORT/mcp` | 真·常驻、一个进程、watcher 永不中断、索引始终热 | 客户端需支持 HTTP 传输（2026 年主流客户端均已支持） |
| B. 常驻进程 + 薄 stdio 转发 | daemon 管 watcher/索引/git，另起 stdio 进程由客户端拉起，经本地 socket 转发 | 兼容纯 stdio 客户端 | 两个进程、复杂度高 |
| C. 纯 stdio 按需（basic-memory 式） | 无守护进程，客户端拉起即工作 | 最简单 | 不符合「常驻」需求；无客户端连接时无人维护索引 |

**实施**：单一二进制 `zipper-agent-memoryd` 两种运行模式——`serve`（常驻 + HTTP，默认）与 `stdio`（按需拉起，供纯 stdio 客户端）；低资源下同时只跑一个实例。

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
- 文件首行（CSV 为表头）作为内容摘要来源（检索片段由 FTS snippet 承担，功能等价）；
- 文件名即浅层检索键：蛇形命名，禁止空格与中文文件名（内容可以中文）；
- 每个文件前几行可含 YAML front-matter（`tags`、`created`、`source`），供 FTS 与索引使用。

## 6. MCP 接口设计

### 6.1 Tools（全部 `memory_` 前缀，蛇形命名，**带行为标注**）

> 行为标注借鉴 basic-memory：`readOnly`（只读）、`destructive`（删除/覆盖）、`idempotent`（可安全重试）。
> 作用：帮助 agent 在调用前判断工具副作用，减少误操作（basic-memory 全工具标注，实践验证有效）。

| Tool | 参数 | 行为 | 说明 |
|------|------|------|------|
| `memory_read` | `path` | readOnly | 读取文件内容（限制必须在 memory/ 内，防路径穿越） |
| `memory_write` | `path, content, overwrite?` | destructive, idempotent | 写入/新建文件；`overwrite=false` 时存在即报错（默认） |
| `memory_append` | `path, content` | destructive | 追加内容（agent 沉淀用，自动加时间戳分隔） |
| `memory_search` | `query, limit?, mode?` | readOnly | FTS5 全文检索；返回 `路径 (score=x): 命中片段`；路径命中词元的文件优先排序；支持别名表（见 §12） |
| `memory_list` | `path?` | readOnly | 列出目录下的文件/子目录 |
| `memory_status` | — | readOnly | 记忆库统计：文件数、总大小、最近变更 |

### 6.2 安全与并发

- **路径沙箱**：所有 `path` 参数解析后必须落在 memory/ 根内，防穿越；
- **并发写**：进程内互斥锁串行化写操作；文件写入采用「临时文件 + rename」原子替换；
- **写链路稳定性是硬要求**（教训源自 basic-memory：move 孤儿 #1152、观察重复/死锁 #1214/#1213 均出在写入链路）：写操作必须原子、可恢复，禁止出现「文件写了但索引没更新/索引更新但文件没写」的半态；任何中途失败只能留下「可被 `rebuild-index` 修复」的状态，且写入顺序固定为：临时文件 → rename → 索引；
- **读不受锁影响**：只读文件，不阻塞。

### 6.3 事件与通知（v1 可选）

- 不实现 MCP notifications（v1 保持精简）；客户端通过轮询 `memory_status` 感知变更。

## 7. 迁移方案（双模式）

1. **目录复制**：直接 `cp -r memory/` 或打包 `zip/tar`，目标机解压后 `zipper-agent-memoryd rebuild-index` 即可使用；
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
- 交付：`fsnotify` 监听（去抖合并）、SQLite FTS5 索引、`rebuild-index`、`serve`
- 验收：修改文件后 ≤2s 索引可见新内容；搜索命中正确；删除文件索引同步移除；`rebuild-index` 后结果一致；常驻内存 < 60MB（`/usr/bin/time -v` 验证）

### 阶段 3：MCP server（serve HTTP + stdio 双模式）
- 交付：实现 §6 全部 6 个 tools（带行为标注）；官方 go-sdk v1.7.x；`serve` 挂载 `/mcp`（默认 `127.0.0.1:8931`，streamable HTTP，JSONResponse 便于调试）+ `stdio` 按需模式；写后同步索引
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
| R1 | MCP Go SDK 选型 | **已解决**：官方 `modelcontextprotocol/go-sdk` v1.7.x（Tier 1）；mark3labs 为备选 |
| R2 | 多 Agent 并发写同一文件 | 进程级互斥 + 原子写；文档约定 agent 只写自己目录 |
| R3 | Agent 自动沉淀格式失控 | 仅通过 `memory_append` 写入 agent/ 目录，格式由工具强约束 |
| R4 | git 仓库随记忆增长膨胀 | autocommit **每日 0 点一次**（默认开启，见决策 6）；定期 `git gc` 提示写入手册 |
| R5 | CSV 与 Markdown 表格混用 | schema 约定同一主题二选一；`memory_status` 可预警 |
| R6 | **basic-memory 为 AGPL-3.0**：只可借鉴设计思想，严禁复制其代码 | 独立实现，代码全部原创；调研报告只做设计参考，不摘录代码 |
| R7 | 索引与文件短暂不一致 | 索引=derived state（eventually consistent），文件=canonical state；不做加锁强一致，可随时 `rebuild-index` |

## 10. 已确认决策（用户拍板 2026-08-21 + 2026-08-22 补充）

| # | 决策 | 结论 |
|---|------|------|
| 1 | ~~语言选型~~ | ✅ Go（D5） |
| 2 | ~~项目名~~ | ✅ ZipperAgentMemory（D7） |
| 3 | ~~git autocommit 默认关闭~~ | ✅ **变更（2026-08-22）：autocommit 默认开启，每日 0 点自动提交一次；用户可主动触发提交（`git-commit` 子命令）** |
| 4 | **MCP 传输形态** | ✅ **方案 A**：单常驻进程 + streamable HTTP；二进制保留 `stdio` 模式（§4.1） |
| 5 | ~~设计整体批准~~ | ✅ v1.0 生效 |
| 6 | **公网部署（2026-08-22）** | ✅ 8931 端口对公网开放（用户已在云安全组放行），**IP 白名单**：`127.0.0.1`（SSH 隧道）+ `120.228.126.4`（家庭直连，动态 IP 变化时改配置文件重启）；支持 JSON 配置文件（`-config`）与 `-allow-ips` flag 双通道 |
| 7 | **首次数据导入（2026-08-22）** | ✅ 密码薄.xlsx（忽略图片）转 CSV 存 `structured/`；服务器地址/root 密码/数据库密码记入记忆库 |
| 8 | **记忆阅读页（2026-10-05）** | ✅ 浏览器只读入口走**路径式** `https://zipper.net.cn/memory/`（复用现有证书，不新建子域、不签新证书）；访问需**长期有效口令**（存服务器文件 `web-token`，0600）；**全库可读**（含 `structured/` 密码资料，口令为唯一防线，**风险经用户知情接受**）；主域其余路径一律 404；阅读页**只读**，无任何写接口。详见 §11 |

> 本设计文档 v1.0 经用户全批准；2026-08-22 补充决策 3/6/7 已获用户确认；2026-10-05 追加 §11 与决策 8 已获用户确认。后续任何偏离须先更新本文档并重新获批（AGENTS.d/workflow.md）。

## 11. 记忆阅读页（只读 Web UI，2026-10-05 追加）

> 需求与验收口径：`docs/features/需求-001-记忆阅读页/`（需求 REQ-01…08、技术方案、实施方案）

### 11.1 目标与边界

| 项 | 内容 |
|---|---|
| 目标 | 让所有者用**浏览器**随时阅读与检索记忆库（外出、临时设备场景） |
| 入口 | `https://zipper.net.cn/memory/`（复用现有 `zipper.net.cn` 证书，不新建子域） |
| 只读 | 仅 GET/登录相关接口；**不提供任何写接口**，写仍走 MCP / CLI |
| 有限写（**需求-006 追加，2026-10-18**） | 仅**回收站三类动作**：软删除 / 恢复 / 清除（含清空）。**全部只接受 POST**，GET 一律 405。安全前提：Cookie 保持 `SameSite=Lax`（跨站 POST 不带凭据）+ `Origin`/`Referer` 同源校验 + 一切文件操作经 `memory.Store` 沙箱。其余写能力仍只走 MCP / CLI |
| 覆盖范围 | **全库可读**（含 `structured/` 密码资料）——口令为唯一防线，**风险经用户知情接受**（决策 8） |
| 暴露面 | 主域除 `/memory/` 外一律 404（含首页，保持下线）；`:80` 的 `/memory/` 301 跳 HTTPS |

### 11.2 架构

```
浏览器 ──https://zipper.net.cn/memory/──► nginx :443（现有证书）
                                            └─ location /memory/ → 127.0.0.1:8932

zipper-agent-memoryd（单进程）
  ├─ :8931  /mcp          MCP（IP 白名单，契约见 §6，本次仅新增 memory_search 的 mode 参数）
  └─ :8932  /memory/*     只读阅读页（口令认证）──► 同一 memory.Store + index.Index 实例
```

- **同进程双监听**：复用同一 store/index，避免第二份索引连接与额外内存（资源红线见 §3.3）；
- Web handler 全部包 panic 恢复，崩溃不影响 MCP 路由；
- 默认 `web.enabled=false`：未配置时行为与旧版完全一致（零影响升级）。

### 11.3 认证

| 项 | 设计 |
|---|---|
| 口令 | 32 字节随机数的 base64url（43 字符），存**独立文件**（默认 `<config 同目录>/web-token`，权限 **0600**，内容一行）；**永不经命令行参数传递**（`ps` 可见） |
| 生成/轮换 | `zipper-agent-memoryd web-token -config <path> -init [-force]`：原子写、自动 0600、屏幕只打印长度不回显口令 |
| 提交方式 | ① 页面登录：`POST /memory/api/login` → 下发持久 Cookie（HttpOnly + SameSite=Lax，HTTPS 下置 Secure，Max-Age 10 年 = 「不过期」）；② 脚本：`Authorization: Bearer`。<br>**为什么必须用 Cookie**：浏览器顶层导航不发送 Authorization 头，纯 Bearer 方案一刷新即掉登录 |
| 校验 | `crypto/subtle` 常量时间比较；失败按**真实来源 IP** 限流（10 次/分钟 → 429，表规模上限 1024）。<br>来源 IP 取法（2026-10-05 决策）：**仅当 `RemoteAddr` 是回环地址时**才采信反代的 `X-Real-IP`（nginx 用 `proxy_set_header` 覆写，不可伪造），其次取 `X-Forwarded-For` 的**最后一段**（nginx 用 `$proxy_add_x_forwarded_for` 追加，末段才是本机观测到的来源）；非回环来源一律用其 `RemoteAddr`，伪造的来源头被忽略——否则反代下所有互联网来源会聚合成**全局单桶**，任意人连发 10 次错误口令即可让所有者 60 秒内无法新登录 |
| 日志 | 只记方法/路径/状态/来源/耗时；**不记 query 与 Cookie**（避免检索词与口令进日志）；路径字段做控制字符消毒，防换行注入伪造日志行 |

### 11.4 路由（全部在 `/memory/` 前缀下）

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET | `/memory/login` | 公开 | 登录页（纯静态，不含库内容） |
| POST | `/memory/api/login` | 公开（限流） | 口令换 Cookie |
| POST | `/memory/api/logout` | 需要 | 清 Cookie |
| GET | `/memory/api/status` | 需要 | 文件数 / 总字节 / 最近修改 |
| GET | `/memory/api/tree?path=` | 需要 | 目录项（不递归，惰性加载） |
| GET | `/memory/api/file?path=` | 需要 | 正文：`markdown`（渲染）/ **`csv`**（`.csv` 逗号、`.tsv` 制表符**解析为表格**：首行作表头、单元格逐个转义、超 5000 行截断并置 `truncated`、解析失败回退 `text`）/ `text`（原文）/ `binary`·`large`（仅提示下载）；内联展示上限 1 MiB |
| GET | `/memory/api/raw?path=` | 需要 | 原始字节下载（`Content-Disposition` 按 RFC 5987 编码中文文件名） |
| GET | `/memory/api/search?q=&mode=&limit=` | 需要 | 全文检索；`mode=and`（默认，全部命中）/`or`（任一命中）；`limit` 上限 100 |
| GET | `/memory/api/trash` | 需要 | **需求-006**：列出回收站（`{count, entries[]{id,path,deleted_at,size}}`）；只读 |
| POST | `/memory/api/delete` | 需要 | **需求-006**：软删除，body `{"path":"notes/a.md"}`；移入 `.trash/<批次>/<原路径>` 并从索引移除 |
| POST | `/memory/api/restore` | 需要 | **需求-006**：恢复，body `{"id":"<批次>/<原路径>"}`；目标已存在则 409 且不覆盖 |
| POST | `/memory/api/purge` | 需要 | **需求-006**：物理删除单条，body `{"id":"..."}` |
| POST | `/memory/api/trash/clear` | 需要 | **需求-006**：清空回收站 |

> **写接口约束（需求-006）**：上述 4 个 POST 接口对任何非 POST 请求返回 `405`（含 GET）；
> `Origin`/`Referer` 存在且与本站不同源时返回 `403`。两者的组合是「不额外输口令」这一决定的安全前提。
| GET | `/memory/static/*` | 需要 | 内嵌静态资源（`go:embed`，无 CDN 依赖） |

未授权响应：`/memory/api/*` → 401；浏览器导航（Accept 含 `text/html`）→ 302 跳登录页；路径穿越 → 400（Linux 绝对路径亦 400，Windows 下为 404，两者都不返回根外内容）；目录路径 / 超长路径 / 含控制字符的路径 → 400；**含点分量的路径（`.git/…`、`.隐藏文件`）→ 404 且不出现在目录列表中**（2026-10-05 用户决策：阅读页不暴露点目录与点文件，含仓库内部；"." 表示根目录仍可用）。

### 11.5 安全边界

1. 所有 `path` 经 `memory.Store` 路径沙箱校验（§6.2 同一实现），**穿越一律拒绝**；
2. Markdown 渲染用 goldmark 且**不启用** `html.WithUnsafe()`：库内从外部抓取的资料里若含原始 HTML/脚本，会被省略为 `<!-- raw HTML omitted -->` 而不执行；
3. 文本类文件由前端以 `textContent` 写入（浏览器负责转义），服务端返回原文，避免二次转义；
4. 响应带 `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Cache-Control: no-store`；
5. **残余风险（用户已知情接受）**：口令即全库（含密码资料）的唯一防线。泄露应对：轮换口令 + 立即修改库内涉及的密码。
6. 阅读页**不暴露点目录/点文件**（`.git/` 等仓库内部不可见、不可读、不可下载）；检索侧天然一致——索引本就不收录隐藏文件（`pkg/index` ShouldIndex）。

7. **精确例外（需求-006）**：`.trash/`（回收站）**仅可经回收站专用接口** `GET /memory/api/trash` 列出；
   通用 `/api/tree`、`/api/file`、`/api/raw` 与检索**仍一律不可达**。这是「接口级例外」，**不是**放宽点目录策略——
   `.git/` 等其它点目录行为完全不变。软删除后的文件因此既不污染目录树、也不污染检索。

### 11.6 配置

```json
{
  "allow_ips": ["127.0.0.1", "120.228.125.194"],
  "autocommit": { "enabled": true, "hour": 0 },
  "web": {
    "enabled": true,
    "addr": "127.0.0.1:8932",
    "token_file": "/opt/zipper-agent-memory/web-token"
  }
}
```

优先级：`-web-addr` flag（显式给出即启用，空串=禁用）> 配置文件 `web.*` > 默认关闭。
启用时口令文件缺失/过短/权限过宽（含 group/other 位）→ **启动快速失败**。

### 11.7 对既有实现的变更

| 项 | 变更 |
|---|---|
| `memory_search`（MCP） | **行为变更**：多关键词由「整串连续短语」改为「按空白切分的词元 AND/OR」；新增可选参数 `mode`（默认 `and`）。单词查询行为不变 |
| `search`（CLI） | 同上（默认 AND） |
| 索引进度 | **无需重建**：FTS5 表结构与单字切分方式未变，仅查询串构造变化 |
| 新增依赖 | `github.com/yuin/goldmark`（纯 Go、MIT、无 CGO，仅用于渲染） |
| `/memory/api/file` 响应 | 新增 `kind: "csv"`（值域扩展）与 `truncated` 字段（表格行数截断标记，2026-10-05 追加 REQ-09）；CSV/TSV 解析在服务端用标准库 `encoding/csv` 完成，逐单元格 `html.EscapeString` 转义后交给前端注入 |
| 资源 | goldmark + 内嵌静态资源约 +1–3MB，仍在 §3.3 的 60MB 红线内 |

## 12. 检索实现（2026-10-18 追加：需求-005 检索质量优化）

> 本章按需求-005 S2 追加（用户授权实施）。前文章节保留为历史记录、不改写；
> 与本章冲突处以**本章为准**——尤其 §11.7 的「索引进度：**无需重建**：FTS5 表结构
> 与单字切分方式未变」仅对需求-001 成立，**自本章起索引内容格式已变更，必须重建**。

### 12.1 切分方式（`indexSchemaVersion = 2`）

| 列 | 切分 | 作用 |
|---|---|---|
| `body` | `tokenizeForFTS`：CJK **单字**切分 | 单字查询可用（AC7 不回归） |
| `body_bg` | `tokenizeBigramForFTS`：**相邻二字组**，标点/空白处断开 | 消除「生，日」被 `"生 日"` 误命中（AC6） |
| `tags` / `created` / `source` | `tokenizeMetaForFTS`：单字 + 二字组**混写** | 元数据（尤其 tags）中文可被 ≥2 字查询命中 |
| `path` | UNINDEXED | 随行存储；路径相关性由 Go 侧加权处理 |

### 12.2 查询构造

- 查询串按空白切词元；每个词元按 CJK / 非 CJK 分段；
- **CJK 段 ≥ 2 字**：逐相邻二字组、以 AND 连接，**绝不回落单字短语**
  （否则「生，日」仍被命中，精确率目标不成立）；
- CJK 段 = 1 字：单字短语；非 CJK 段：原样作为词；
- 条件**不限定列名**——二字组 token 只存在于 `body_bg` 与元数据列，单字 token
  只存在于 `body` 与元数据列，两类天然不混淆；
- 词元之间按 `mode` 以 AND / OR 连接。

### 12.3 别名表（可选）

`memory/meta/synonyms.tsv`：制表符分隔、组内**对称扩展**（搜任一成员命中整组）；
文件缺失 / 坏行一律静默降级为「无别名表」（检索行为与未启用时一致）；单组 ≤10 成员；
查询时按 stat + mtime 缓存加载，**改文件后下一次检索即生效，无需重建索引**。

### 12.4 排序与分数

路径命中查询词元的行优先，其次按 `-bm25(...)` 降序（Go 侧字典序排序保证 AC3）。
`Result.Score` **仅在同一次查询内**可比较，不跨查询、不跨库。
bm25 列权重：`tags 10 / created 1 / source 1 / body 4 / body_bg 6 / path 0`
（path 为 UNINDEXED，权重仅占位对齐列数）。

### 12.5 索引版本保护（不静默降级）

`indexSchemaVersion` 与 `PRAGMA user_version` 比对：版本不符且库内已有数据 ⇒
`Search` / `Upsert` 返回 `ErrIndexStale`，须执行 `rebuild-index`；空库在首次打开时
自动标记为当前版本；版本号与索引数据**同事务**写入，避免版本与实际内容脱节。

### 12.6 实测（2026-10-18，本机）

| 指标 | 结果 | 红线 |
|---|---|---|
| 索引体积 | 589,824 → 692,224 字节（**1.17×**，远低于预估的 2.5–3×） | — |
| `serve` 常驻 RSS | **20.3 MB** | < 60 MB ✅ |
| 测试 | `go test -count=1 ./cmd/... ./pkg/...` 全绿 | — |

**不引入任何新依赖；不引入向量库 / embedding** —— §3.3 的「明确避免」继续有效。


