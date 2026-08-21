# ZipperAgentMemory

AI 跨平台永久性记忆工具：在 Linux 低资源服务器（或任何 Go 可运行平台）上运行的**常驻进程**，维护一个 **Markdown + CSV 文件型记忆库**，经 **MCP**（streamable HTTP / stdio 双模式）供 Claude / Cursor / DSH 等 Agent 读写与检索。

- 记忆库即普通目录树（文件是真源），索引是**可随时重建的派生状态**（derived state）；
- 单 Go 二进制、零 CGO、零运行时依赖，常驻内存目标 **< 60MB**；
- 迁移支持 **目录打包** 与 **git 同步（bundle 单文件）** 双模式；
- 版本：`zipper-agent-memoryd` **v0.4.0**（阶段 5 交付，全功能完成）。

```
┌─────────────────────┐   streamable HTTP / stdio   ┌────────────────────────────────┐
│ 任意 MCP 客户端      │ ◄─────────────────────────► │ zipper-agent-memoryd (Go 单二进制) │
│ Claude/Cursor/DSH…  │                             │  ├─ MCP server（6 个 memory_* 工具）│
└─────────────────────┘                             │  ├─ fsnotify watcher（去抖合并）    │
                                                    │  ├─ SQLite FTS5 索引（WAL）        │
                                                    │  └─ git autocommit（可选开关）      │
                                                    └──────────────┬─────────────────┘
                                                                   │ 读写
                                                    ┌──────────────▼─────────────────┐
                                                    │ memory/  ← git 仓库（可选）      │
                                                    │   README.md  index.md          │
                                                    │   notes/ projects/ structured/ │
                                                    │   agent/ meta/                 │
                                                    └────────────────────────────────┘
```

## 目录

- [快速开始](#快速开始)
- [配置项](#配置项)
- [MCP 客户端接入](#mcp-客户端接入)
- [迁移指南](#迁移指南)
- [资源约束](#资源约束)
- [开发指南](#开发指南)
- [文档索引](#文档索引)

---

## 快速开始

### 1. 构建

要求 **Go ≥ 1.25**（官方 go-sdk v1.7.x 要求；本项目 go.mod 声明 `go 1.26`），git（可选，autocommit/迁移用）。

```bash
# 两个二进制：守护进程 + CLI
go build -o bin/zipper-agent-memoryd ./cmd/zipper-agent-memoryd
go build -o bin/zam ./cmd/zam

# 或使用 Makefile（Windows 需 GNU Make）
make build zam
```

### 2. 准备记忆库

`memory/` 目录已内置骨架（README / index.md / notes / projects / structured / agent / meta），或自行创建：

```bash
mkdir -p memory/{notes,projects,structured,agent,meta}
```

可选：把 `memory/` 初始化为 git 仓库（autocommit、bundle 迁移的前置）：

```bash
./bin/zipper-agent-memoryd git-init -root memory
# git-init: repository ready at ...（幂等；自动补全本地 user.name/email，不碰全局配置）
```

### 3. 启动守护进程（常驻 + HTTP，推荐）

```bash
./bin/zipper-agent-memoryd serve -root memory
```

启动时**全量重建索引**，随后 fsnotify 监听变更增量维护；同时提供 MCP 服务于 `http://127.0.0.1:8931/mcp`。Ctrl+C 优雅退出。

可选组合：

```bash
# 自定义监听地址 + 开启 git autocommit（默认关闭）
./bin/zipper-agent-memoryd serve -root memory -addr 127.0.0.1:8931 -git-autocommit
```

### 4. MCP stdio 模式（按需拉起，供纯 stdio 客户端）

```bash
./bin/zipper-agent-memoryd stdio -root memory
```

客户端断开即退出；协议走 stdout，运行日志走 stderr。与 serve 不同，stdio 模式**不装配 watcher/autocommit**（会话期短，无事件流）。

### 5. CLI 辅助命令

```bash
# 全量重建索引（索引是 derived state，迁移后/怀疑不一致时执行）
./bin/zipper-agent-memoryd rebuild-index -root memory

# 命令行检索（输出 路径+命中片段）
./bin/zipper-agent-memoryd search -root memory "Go 语言"

# 记忆库文件读写 CLI
./bin/zam write notes/hello.md "hello world"
./bin/zam read notes/hello.md
./bin/zam append agent/dev/2026-08.md "一行沉淀"
./bin/zam list notes
./bin/zam status

# 版本
./bin/zipper-agent-memoryd version
```

## 配置项

### `zipper-agent-memoryd` 通用 flag（全部子命令）

| flag | 默认 | 说明 |
|------|------|------|
| `-root <dir>` | `./memory` | 记忆库根目录 |
| `-db <path>` | memory/ 同级 `.zipper-agent-memory.index.sqlite` | 索引数据库路径。默认放根目录**同级**（不进入被监听的目录树，避免索引写自身触发监听事件；也保持 memory/ 仓库干净） |

### `serve` 专属 flag

| flag | 默认 | 说明 |
|------|------|------|
| `-addr <host:port>` | `127.0.0.1:8931` | MCP streamable HTTP 监听地址 |
| `-debounce <dur>` | `500ms` | 文件事件去抖窗口（滑动窗口，事件静默该时长后批量入库） |
| `-git-autocommit` | 关 | 开启 git autocommit：变更去抖批次后自动 `git add -A && commit`。仓库未初始化时自动 `git init` 并设置本地身份（不碰全局配置）。**默认关闭，显式开启** |

### `search` 专属 flag

| flag | 默认 | 说明 |
|------|------|------|
| `-limit <n>` | `20` | 最多返回命中条数 |

### `zam` CLI

通用 `-root <dir>`（默认 `./memory`）；`write` 另有 `-overwrite`（默认拒绝覆盖已存在文件）。

## MCP 客户端接入

工具清单（全部带行为标注，`design.md §6.1`）：

| 工具 | 参数 | 行为 | 说明 |
|------|------|------|------|
| `memory_read` | `path` | readOnly | 读取 memory/ 内文件（防路径穿越） |
| `memory_write` | `path, content, overwrite?` | destructive, idempotent | 写入/新建；`overwrite=false` 时存在即报错（默认），原子覆盖 |
| `memory_append` | `path, content` | destructive | 追加（自动时间戳分隔行）；不存在自动创建 |
| `memory_search` | `query, limit?` | readOnly | FTS5 全文检索（中文按单字切分），返回路径+命中片段 |
| `memory_list` | `path?` | readOnly | 列出目录项（缺省=根目录，不递归） |
| `memory_status` | — | readOnly | 统计：文件数/总大小/最近变更 |

> 所有 `path` 必须是 memory/ 内的相对路径（正斜杠）；`../`、绝对路径、符号链接/junction 逃逸一律拒绝。

### 方式 A：HTTP（streamable HTTP，常驻进程）

需先启动 `serve`（守护进程常驻），客户端配置指向 `http://127.0.0.1:8931/mcp`。适用于**常驻维护**诉求：watcher 永不中断、索引始终热。

**Claude Desktop**（`claude_desktop_config.json`）：

```json
{
  "mcpServers": {
    "zipper-agent-memory": {
      "url": "http://127.0.0.1:8931/mcp"
    }
  }
}
```

**Cursor**（项目根 `.cursor/mcp.json`）：

```json
{
  "mcpServers": {
    "zipper-agent-memory": {
      "url": "http://127.0.0.1:8931/mcp"
    }
  }
}
```

### 方式 B：stdio（按需拉起）

客户端每次 spawn 进程、会话结束退出；适合纯 stdio 客户端与无常驻进程场景。二进制路径与 `-root` 按实际部署填写（示例为 Linux 部署路径）。

**Claude Desktop**：

```json
{
  "mcpServers": {
    "zipper-agent-memory": {
      "command": "/opt/zipper-agent-memory/zipper-agent-memoryd",
      "args": ["stdio", "-root", "/opt/zipper-agent-memory/memory"]
    }
  }
}
```

**Cursor**（`.cursor/mcp.json`）：

```json
{
  "mcpServers": {
    "zipper-agent-memory": {
      "command": "/opt/zipper-agent-memory/zipper-agent-memoryd",
      "args": ["stdio", "-root", "/opt/zipper-agent-memory/memory"]
    }
  }
}
```

> 提示：低资源服务器上**同时只跑一个实例**（serve 常驻 或 客户端按需 stdio，二选一）。HTTP 模式响应为 `application/json`，可用 MCP Inspector / curl 直接调试。

## 迁移指南

迁移脚本 `scripts/migrate.sh` 支持双模式（`design.md §7`）；目标机还原后**必须重建索引**（索引是 derived state）。

```bash
# 模式 1：目录打包（纯文件，不含 git 历史）
./scripts/migrate.sh pack   <memory_dir> [输出文件]          # 默认 <目录名>.tar.gz（缺 tar 回退 zip）
./scripts/migrate.sh restore <来源> <目标目录> [--rebuild-index]

# 模式 2：git 同步（无远程时 bundle 单文件，含全部历史）
./scripts/migrate.sh bundle <memory_dir> [输出文件]          # 要求已是 git 仓库且 ≥1 提交
./scripts/migrate.sh restore <来源> <目标目录> [--rebuild-index]
```

完整链路示例（源机打包 → 目标机还原 → 检索验证）：

```bash
# 源机
./scripts/migrate.sh pack memory memory.tar.gz

# 目标机（把 tar.gz 传过去后）
./scripts/migrate.sh restore memory.tar.gz memory --rebuild-index
zipper-agent-memoryd search -root memory "关键词"
```

- bundle 前置：`zipper-agent-memoryd git-init -root memory` 初始化仓库；autocommit 或手动 `git commit` 产生首个提交后再 bundle；
- 环境变量 `DRY_RUN=1` 只打印将执行的命令；
- `ZAM_BIN` 指向二进制路径，`--rebuild-index` 自动重建；还原目录必须不存在或为空。

## 资源约束

| 指标 | 目标 | 手段 |
|------|------|------|
| 常驻内存 | **< 60MB**（阶段 2 实测 11.84MB） | Go 单进程；索引仅路径+元数据+FTS 内容 |
| 空闲 CPU | ≈0 | fsnotify 事件驱动 + 去抖合并，无轮询 |
| 磁盘 | 记忆文件 + 索引（估文件体积 10–20%） | SQLite 单文件（WAL） |
| 明确避免 | 向量库、embedding、Elasticsearch、watchman | 全部不引入 |

git autocommit 默认关闭（显式 `-git-autocommit` 开启）；仓库随记忆增长膨胀时**手动**执行 `git gc`（进程只提示、从不自动执行，design R4）。

## 开发指南

```bash
# 全量单元测试（关键路径：路径沙箱/原子写/索引/MCP 参数校验/autocommit）
go test -count=1 ./...

# 静态检查与格式
go vet ./...
gofmt -l .          # 应无输出

# 端到端测试（真实二进制 + stdio/HTTP 双模式握手，CI 之外手动跑）
go test -tags e2e ./integration/ -v

# 构建
go build ./...
```

代码规范：`docs/standards/go-编码规范.md`（gofmt 为准、错误包装、参数数组调外部命令、原子写顺序「临时文件 → rename → 索引」、资源红线）。变更管理遵循 `AGENTS.d/` 多 Agent 协作约定：先文档后编码、未批准不实施、提交走 Conventional Commits（由协调者统一执行）。

## 文档索引

| 文档 | 说明 |
|------|------|
| `docs/design.md` | **唯一权威设计文档**（v1.0 已批准） |
| `docs/standards/go-编码规范.md` | Go 编码规范 |
| `docs/部署手册.md` | 服务器从零到跑通的部署步骤（systemd 托管） |
| `docs/research/` | 调研报告（basic-memory、MCP Go SDK 选型） |
| `docs/memory/` | 项目记忆仓库（状态快照、服务器评估） |
| `docs/验收/` | 各阶段验收记录 |
| `AGENTS.d/` | 多 Agent 协作约定 |
