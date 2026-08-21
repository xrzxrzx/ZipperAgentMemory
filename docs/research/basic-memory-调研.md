# basic-memory 调研报告

> 调研日期：2026-08-21
> 调研对象：开源项目 basic-memory（basicmachines-co/basic-memory，曾用名 Brennall/basic-memory）
> 调研方式：一手来源——GitHub 仓库源码/README/AGENTS.md/NOTE-FORMAT.md、PyPI 元数据、官方文档站 docs.basicmemory.com（llms.txt / llms-full.txt / 各 raw 页面）、GitHub Issues/Releases API

## 来源列表（一手）

- GitHub 仓库（README、AGENTS.md、CHANGELOG、源码）：<https://github.com/basicmachines-co/basic-memory>
- 官方文档站（llms.txt 索引）：<https://docs.basicmemory.com/llms.txt>；完整文档 <https://docs.basicmemory.com/llms-full.txt>；本地快速开始 <https://docs.basicmemory.com/raw/start-here/quickstart-local.md>
- PyPI 包页与 JSON 元数据：<https://pypi.org/project/basic-memory/>；<https://pypi.org/pypi/basic-memory/json>
- 官方博客/营销站：<https://basicmemory.com>；公司 <https://basicmachines.co>
- GitHub Issues / Releases API：<https://api.github.com/repos/basicmachines-co/basic-memory>

---

## 1. 概述

basic-memory 是一个「本地优先」的 AI 持久记忆工具：核心是一堆 **普通 Markdown 文件 + 一个本地 SQLite 索引**，通过 **MCP（Model Context Protocol）** 服务器暴露给 Claude Desktop / Claude Code / Codex / Cursor / ChatGPT 等 AI 客户端，让 LLM 和人读写同一批文件，并从文件中的观察（Observation）与双链（Relation）自动构建可遍历的知识图谱。

一句话定位（README 原文）：*"Your knowledge lives as Markdown files that both you and your AI can read, write, and search… Just files plus a local SQLite index. No servers required."* [README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)

关键事实：**文件是权威数据源（source of truth），SQLite 只是派生索引**。AGENTS.md 明确写着 *"SQLite is used for indexing and full text search, files are source of truth"*。[AGENTS.md](https://github.com/basicmachines-co/basic-memory/blob/main/AGENTS.md)

- 语言：Python（≥3.12），异步栈（SQLAlchemy 2.0 async + aiosqlite + FastAPI + FastMCP 3.3.1）
- 许可：AGPL-3.0（强 copyleft，对「借鉴代码」有法律约束，见第 7 节）
- 现状：3698 stars / 259 forks / 72 个 open issues，最近 push 2026-08-21，发布活跃（v0.22.1 于 2026-06-13，v0.23.0 已在筹备，issue #1288）[GitHub API](https://api.github.com/repos/basicmachines-co/basic-memory)
- 商业模式：同一引擎的托管云（basicmemory.com，$15/月）与本地免费版并存；README 大篇幅推广云服务

---

## 2. 存储结构

### 2.1 目录布局

- **项目（Project）** 是知识隔离边界：一个项目 = 一个目录。默认项目目录为 `~/basic-memory`（笔记默认存在这里）[quickstart-local](https://docs.basicmemory.com/raw/start-here/quickstart-local.md)。
- 目录内按用户/LLM 指定的 `directory` 参数建子文件夹，官方示例布局：

```
~/basic-memory/
├── My First Note.md
├── projects/  API Design.md
├── research/  Database Optimization.md
└── meetings/  Team Standup 2026-06-15.md
```

- 全局配置与数据库在 `~/.basic-memory/`（可用 `BASIC_MEMORY_CONFIG_DIR` / `XDG_CONFIG_HOME` 覆盖）：`memory.db`（SQLite 索引库）、`config.json`、`basic-memory.log`、`fastembed_cache/`（语义模型缓存）[config_models.py 源码](https://github.com/basicmachines-co/basic-memory/blob/main/src/basic_memory/config_models.py)、[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)

### 2.2 文件命名

- 文件名由 title 生成（slug 化），标题"Meeting Notes" → `Meeting Notes.md`；系统级规则见 permalink 生成：路径转小写、空格/下划线转连字符、**保留非 ASCII（如中文）**、保留版本号中的点（`Version 2.0.0` → `version-2.0.0`）[utils.py 源码](https://github.com/basicmachines-co/basic-memory/blob/main/src/basic_memory/utils.py)
- **permalink**：从文件路径派生的稳定语义 ID（`docs/My Feature.md` → `docs/my-feature`），frontmatter 可显式覆盖；文件移动后 permalink 不跟随路径变化（除非配置允许）。permalink 是 `memory://` URL 的寻址基础 [NOTE-FORMAT.md](https://github.com/basicmachines-co/basic-memory/blob/main/NOTE-FORMAT.md)

### 2.3 笔记格式（front-matter + 正文）

每篇笔记 = YAML frontmatter + 正文（观察）+ 关系（relation）[NOTE-FORMAT.md](https://github.com/basicmachines-co/basic-memory/blob/main/NOTE-FORMAT.md)：

```markdown
---
title: Coffee Brewing Methods
type: note
tags: [coffee, brewing]
permalink: coffee-brewing-methods
---

# Coffee Brewing Methods

## Observations
- [method] Pour over provides more flavor clarity than French press
- [technique] Water temperature at 205°F extracts optimal compounds #brewing
- [preference] Ethiopian beans work well with lighter roasts (personal experience)

## Relations
- relates_to [[Coffee Bean Origins]]
- requires [[Proper Grinding Technique]]
```

**Frontmatter 标准字段**：`title`（缺省=文件名）、`type`（缺省 `note`，实体类型，参与 schema 解析与过滤）、`tags`（数组或逗号串）、`permalink`（稳定 ID）、`schema`（Picoschema 挂载）；**任意自定义字段**会存入 `entity_metadata` 并进入搜索索引（如 `status: active`、`source: wikipedia`）。[NOTE-FORMAT.md](https://github.com/basicmachines-co/basic-memory/blob/main/NOTE-FORMAT.md)

**解析宽容性**：`## Observations` / `## Relations` 标题只是惯例而非必需——解析器按语法模式（`- [category] ...`、`- type [[Target]]`）在全文任意位置识别观察与关系。

### 2.4 知识图谱表示（观察/实体/关系）

整个语法只有三样东西（README 原文：*"Each file is an Entity. Entities have Observations and Relations. That's the whole grammar."*）[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)：

- **Entity（实体）= 一个文件**。数据库里 `entity` 表存 id、external_id（UUID，稳定）、title、note_type、permalink、file_path、checksum、mtime/size（变更检测）等；关系表/观察表均挂在 entity 上 [models/knowledge.py 源码](https://github.com/basicmachines-co/basic-memory/blob/main/src/basic_memory/models/knowledge.py)
- **Observation（观察）= 带分类的事实**，列表项语法 `- [category] content #tag1 #tag2 (context)`；分类可任意（method/tech/decision/fact…），重复分类构成数组字段；checkbox（`- [ ]`）、Markdown 链接、裸双链不当作观察 [NOTE-FORMAT.md](https://github.com/basicmachines-co/basic-memory/blob/main/NOTE-FORMAT.md)
- **Relation（关系）= 有向语义链接**，`- relation_type [[Target]]`；缺省类型为 `relates_to`；正文 prose 中的裸 `[[Target]]` 产生隐式 `links_to`；关系类型是约定而非固定集合（implements/depends_on/part_of/contains/works_at…），多词类型可用引号 `- "pairs well with" [[Target]]`
- **前向引用**：可链接到尚不存在的实体，目标创建后自动解析
- **记忆寻址**：`memory://permalink`、`memory://Title`、`memory://project/path`，支持通配符 `memory://auth*`、`memory://*/approaches`（供 build_context 遍历图）
- **Schema 系统**（进阶）：用 Google Dotprompt 的 Picoschema 声明笔记结构（`name: string, full name`、`works_at?: Organization`），三种挂载方式（frontmatter 内联 dict / 字符串引用 schema 笔记 / 按 type 隐式匹配），支持 `schema_infer`（按频率推断）、`schema_validate`、`schema_diff`（漂移检测）[NOTE-FORMAT.md](https://github.com/basicmachines-co/basic-memory/blob/main/NOTE-FORMAT.md)

---

## 3. MCP 工具集

### 3.1 工具清单（v0.22.1，源码 `src/basic_memory/mcp/tools/__init__.py`）

| 类别 | 工具 | 功能 |
|---|---|---|
| 内容读写 | `write_note(title, content, directory, tags, note_type, metadata, overwrite)` | 创建/覆盖笔记；默认拒绝覆盖已有文件（防误写），增量更新走 edit_note |
| | `read_note(identifier, page, page_size)` | 按 title/permalink/memory:// URL 读笔记，带知识图谱上下文 |
| | `read_content(path)` | 读**原始文件**（文本/图片/二进制），不经图谱处理 |
| | `view_note` | 以格式化 artifact 形式展示（可读性优化） |
| | `edit_note(identifier, operation, content)` | 增量编辑：append / prepend / find_replace / replace_section |
| | `move_note` / `delete_note` | 移动/删除笔记或目录，同步维护 DB 与链接 |
| 搜索与发现 | `search_notes(query, search_type, page, page_size, types, entity_types, after_date, metadata 过滤)` | FTS / 语义 / 混合检索，返回含匹配片段上下文 |
| | `recent_activity(type, depth, timeframe)` | 按时间窗取最近更新（会话开场定位用） |
| | `list_directory(dir_name, depth, file_name_glob)` | 浏览目录结构 |
| 知识图谱 | `build_context(url, depth, timeframe)` | 沿 memory:// URL 遍历图，构建会话上下文注入 |
| 项目管理 | `list_memory_projects` / `create_memory_project` / `delete_project` / `list_workspaces` | 多项目/工作区管理 |
| Schema | `schema_validate` / `schema_infer` / `schema_diff` | 结构校验、推断、漂移检测 |
| 兼容与诊断 | `search(query)` / `fetch(id)` | OpenAI Actions 兼容版（ChatGPT Custom GPT 用） |
| | `basic_memory_diagnostics` | 环境诊断 |

另有 5 个 **MCP Prompts**（引导 LLM 用工具）：`ai_assistant_guide`（使用指南）、`continue_conversation`（跨会话续接）、`getting_started`（首次连接引导）、`recent_activity`、`search`；以及 `memory://` **Resources**（`project_info` 等）。

所有工具带 **MCP 行为标注**（`readOnlyHint` / `destructiveHint` / `idempotentHint` / `openWorldHint`，FastMCP 3.0），让 Agent 渐进式发现能力、不浪费 token 试错；默认输出纯文本，传 `output_format="json"` 取结构化结果。[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)、[mcp/tools 源码目录](https://github.com/basicmachines-co/basic-memory/tree/main/src/basic_memory/mcp/tools)

### 3.2 与 Claude Desktop / Claude Code 的集成方式

**不是纯 autoload，而是「MCP 工具常驻 + 引导性 prompts + 插件钩子」三层**：

1. **Claude Desktop**：在 `claude_desktop_config.json` 注册 `"command": "uvx", "args": ["basic-memory", "mcp"]`（stdio 传输）→ Claude 启动时拉起 MCP 服务器，工具自动出现在模型可用工具列表里 [README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)
2. **Claude Code**：`claude mcp add basic-memory -- uvx basic-memory mcp`；另提供**官方 Claude Code 插件**（marketplace 安装）：SessionStart 钩子在会话开始时做简报（briefing）、PreCompact 钩子在压缩前写检查点、`/basic-memory:bm-setup` `:remember` `:share` `:status` 斜杠命令 [README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)
3. **首次连接引导**：`getting_started` prompt + 空状态引导，教模型先 `recent_activity` 定位、再搜索、再读写（issue #1145 实现）[ai_assistant_guide.md](https://github.com/basicmachines-co/basic-memory/blob/main/src/basic_memory/mcp/resources/ai_assistant_guide.md)

其它客户端：Codex（config.toml）、Cursor（.cursor/mcp.json）、VS Code、ChatGPT（Custom GPT 的 search/fetch）、Obsidian（直接打开同一目录，无需 MCP）[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)。

---

## 4. 检索机制

**双轨：纯文本 FTS + 可选语义向量，均建索引（不是裸文件搜索），索引在 SQLite 里。**

### 4.1 全文检索（默认、离线、零模型）

- SQLite **FTS5 虚拟表** `search_index`：字段含 title、content_stems、content_snippet（命中片段）、permalink、file_path、type、relation 字段（from_id/to_id/relation_type）、observation 字段（entity_id/category）、metadata（JSON，可过滤）；`tokenize='unicode61 tokenchars 0x2F'`（`/` 参与分词，支持路径检索）、`prefix='1,2,3,4'`
- Postgres 后端用 `tsvector GENERATED ALWAYS` + GIN 索引替代 [models/search.py 源码](https://github.com/basicmachines-co/basic-memory/blob/main/src/basic_memory/models/search.py)
- 搜索支持：关键词、路径、类型/实体类型/日期/元数据过滤、分页

### 4.2 语义检索（可选但默认开启）

- **sqlite-vec**（本地 SQLite 的 `vec0` 虚拟表）存 embedding；Postgres 用 pgvector 或 Milvus（`basic-memory[milvus]` 可选 extra）
- Embedding：**FastEmbed（ONNX 本地推理）**，默认模型 `bge-small-en-v1.5`（BAAI），首次使用自动下载到 `~/.basic-memory/fastembed_cache`；也可配 LiteLLM 接云端 embedding [config_models.py](https://github.com/basicmachines-co/basic-memory/blob/main/src/basic_memory/config_models.py)
- `semantic_search_enabled` 默认值 = 依赖可导入即为 True（即装即用），可用 `BASIC_MEMORY_SEMANTIC_SEARCH_ENABLED` 控制
- **Hybrid 混合检索**：FTS + 向量融合排序
- **Rerank 重排（默认关闭）**：局部 FastEmbed cross-encoder（`jinaai/jina-reranker-v1-tiny-en`）或 LiteLLM 托管 reranker；开启会加推理延迟与首次模型下载 [README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)
- 索引维护：文件变更由进程内 watchfiles 文件监视器增量索引；`bm reindex`（可 `--embeddings` 重建向量）；`bm doctor` 做文件↔DB 一致性检查；孤儿实体清理 [AGENTS.md](https://github.com/basicmachines-co/basic-memory/blob/main/AGENTS.md)

---

## 5. 资源占用与运行方式

### 5.1 语言与依赖（重）

- Python ≥3.12（3.12–3.15）；PyPI wheel 本体仅 0.9 MB（纯 Python），但**直接依赖约 40 个**：fastapi、fastmcp==3.3.1、mcp>=1.23、sqlalchemy、alembic、aiosqlite、sqlite-vec、fastembed、litellm、openai、logfire、loguru、typer、rich、watchfiles、httpx、psutil、pillow、pybars3、pydantic、dateparser、unidecode 等 [PyPI JSON](https://pypi.org/pypi/basic-memory/json)
- 语义功能（fastembed/sqlite-vec）已在基础依赖里，装完即占体积；Postgres/Milvus 走可选 extra

### 5.2 运行形态

- **本地默认：按需启动的 MCP stdio 服务器**——`uvx basic-memory mcp` 由 AI 客户端（如 Claude Desktop）在会话启动时拉起，会话结束进程退出；不是常驻系统服务，空闲不占资源 [README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)、[cli/commands/mcp.py](https://github.com/basicmachines-co/basic-memory/blob/main/src/basic_memory/cli/commands/mcp.py)
- 服务器进程内运行：文件监视器（watchfiles）、索引、FastAPI 应用（MCP 工具经 in-process ASGI 调用 API）
- 也支持 **streamable-http / SSE** 常驻模式（`basic-memory mcp --transport streamable-http --port 8000`）、Docker 部署（官方镜像 `ghcr.io/basicmachines-co/basic-memory`，python:3.12-slim + uv）[docs/Docker.md](https://github.com/basicmachines-co/basic-memory/blob/main/docs/Docker.md)
- CLI 命令（bm project/status/doctor/reindex/import…）按需执行；`uv tool` 安装自动更新（每 24h 检查，可关）[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)

### 5.3 内存/CPU 开销量级

官方未发布基准数字（仓库 benchmarks 目录含 sync 性能测试但无内存规格）。从架构可推导：

- 纯 FTS 模式（关语义）：SQLite 索引 + 异步运行时，量级轻；这是 README 宣称的 "No servers required" 场景
- 开语义搜索：bge-small-en-v1.5 ONNX 模型约百 MB 级磁盘（首次下载），每次查询跑本地推理，CPU 有额外开销；rerank 再叠加。README 明确 rerank 默认关闭、语义在低配场景是**可选项**而非必须
- 官方已知的针对性优化项（issue #382「大文件同步内存优化」仍 open）说明大文件场景内存曾有问题 [issue #382](https://github.com/basicmachines-co/basic-memory/issues/382)

---

## 6. 迁移与备份

官方对本地与云给出**两套不同答案**：

- **本地（官方明说「自己管」）**：README 对比表写本地备份 *"Roll your own"*、跨设备同步 *"Manual (Git, Syncthing, etc.)"*；文档写 *"locally, notes are plain files you can track with git"*。因为文件即数据，备份 = 备份 Markdown 目录（SQLite 索引可随时 `bm reindex` 重建）[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)、[llms-full.txt](https://docs.basicmemory.com/llms-full.txt)
- **云（付费）**：自动每日快照 + 手动快照（`bm cloud snapshot create/list/restore`）、每笔记 File History 版本历史、`bm cloud push/pull`（git 风格、加性传输、`--on-conflict` 处理冲突）[cloud-snapshots](https://docs.basicmemory.com/cloud/cloud-snapshots)
- **迁移/导出**：官方宣称无 lock-in——笔记是标准 Markdown，随时导出；内置导入器：`bm import claude conversations`、`bm import chatgpt`、`bm import memory-json`；跨大版本升级注意 v0.19 的配置变更说明（release notes 有专门段落）[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)
- 一致性工具：`bm doctor`（文件↔DB 校验）、`bm reindex`（重建索引）[README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)

---

## 7. 局限与维护状态

### 7.1 维护状态（活跃）

- 3698 stars / 259 forks / 72 open issues；最近提交 2026-08-21；v0.22.1（2026-06-13）、v0.22.0、v0.21.6 等发布节奏约每月一次；v0.23.0 已在筹备（issue #1288）；未归档 [GitHub API](https://api.github.com/repos/basicmachines-co/basic-memory)
- 团队在 README 顶部大力推广付费云（$15/月），开源版与云同源同格式

### 7.2 已知问题与高频抱怨（来自 issues/PR 一手记录）

**Bug 类（多数已修，说明踩过的坑）：**

- 笔记卡在 "modified" 状态不落盘（#72）；write_note 在某些场景更新已有文件失败（#71）——早期写入链路不稳定的痕迹
- move_note 产生孤儿文件/索引（#1152 修复，25 条评论的高热度 bug）；macOS 大小写不敏感改名导致重复索引（#1281，open）
- 观察重复写入（#1214）、生产死锁集群（#1213/#1224）——并发写入是该项目反复出问题的区域
- CLI 挂起（#504）；大小写不一致导致项目切换失败（#127）；文件夹结构混乱（#172）；ChatGPT 导入 KeyError（#1276）
- FastEmbed 模型缓存损坏会令语义搜索失败（源码 search.py 有专门的自愈与提示路径）

**未决的功能性局限（open）：**

- 大文件同步内存优化（#382）；PDF/图片等非 Markdown 资源支持（#543）；基于 git 的撤销/恢复（#124，4 个 👍）；frontmatter 权限/校验强制（#993）；实体的 created_at/updated_at 回写 frontmatter（#684）
- 跨会话实体混淆导致的检索失败在 LoCoMo 基准上是公开短板（#951）

**结构性局限（值得关注）：**

1. **AGPL-3.0 强 copyleft**——直接抄代码会污染闭源/商业项目；只能借鉴设计
2. **Python ≥3.12 + ~40 直接依赖**，低资源服务器上部署成本不低；`uv` 管理
3. **语义搜索默认开启**意味着装完首次使用要下模型（约百 MB），无网环境需显式关闭（`BASIC_MEMORY_SEMANTIC_SEARCH_ENABLED=false` 或纯 FTS 模式）
4. **遥测**：默认收集匿名漏斗事件（云推广展示/登录尝试），`BASIC_MEMORY_NO_PROMOS=1` 可全部关闭；不收集笔记内容 [README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md)

---

## 8. 对我们的借鉴点（Go 实现、低资源服务器、Markdown + MCP）

**值得借鉴：**

1. **「文件为源、SQLite 为派生索引」架构**——与我们的设想完全一致；他们的实践证明了 FTS5 + 增量索引 + `reindex`/`doctor` 自愈命令是低资源下正确的取舍。无外部服务、无常驻进程（stdio 按需拉起）是低资源服务器的最优形态
2. **观察/关系 Markdown 语法**——`- [category] content #tag (context)` 与 `- relation_type [[Target]]` 极简、对人类和 LLM 都自然、解析规则明确（checkbox/链接/裸双链的排除规则可直接照搬设计）
3. **permalink 稳定寻址 + `memory://` URL 模式**（按 permalink/title/path、支持通配符）——比纯文件路径寻址更健壮，值得借鉴
4. **frontmatter 自定义字段进索引**（entity_metadata）——免 schema 约束就获得结构化过滤
5. **MCP 工具行为标注**（readOnly/destructive/idempotent/openWorld hints）与 `output_format=json` 开关——省 token、利于渐进式发现
6. **引导式集成**（getting_started prompt + recent_activity 开场 + 插件钩子做会话简报/压缩前检查点）——解决「工具可用但 Agent 不会用」的落地问题
7. **Schema 系统（Picoschema）设计**：推断/校验/漂移三件套，先有笔记再沉淀结构，比强 schema 更适合个人知识库

**不适合/需要避开（低资源视角）：**

1. **Python 技术栈本身**（≥3.12、~40 依赖、async 运行时）——Go 单二进制 + 内嵌 SQLite 更契合低资源服务器，这正印证了我们选 Go 的方向
2. **默认开启的语义向量搜索**（FastEmbed ONNX + bge-small + 可选 rerank）——低资源服务器应默认纯 FTS，向量检索作为显式可选特性，且模型要小
3. **功能蔓延**：云/Postgres/Milvus/Redis 缓存/团队协作——对个人低资源部署是负担，不做
4. **AGPL-3.0 许可**——不可复制其代码，仅参考设计；且他们的商业云（快照/备份/同步）与本地体验割裂，本地备份要靠用户自理，我们的产品应把备份（如 git 自动提交）做成内置
5. **写入链路的稳定性教训**——move/rename/并发写/孤儿清理是他们的 bug 高发区（#1152/#1214/#1281），我们的实现要在文件操作上做原子写与校验，宁可简单可靠

---

## 结论（300 字内）

basic-memory 验证了「Markdown 文件为源 + SQLite 派生索引 + MCP 访问」是 AI 永久记忆的正确架构：纯文件无 lock-in、双链+观察语法简单可解析、FTS5 检索零外部服务、stdio 按需启动不占常驻资源——这些都值得直接借鉴，且与我们 Go 方案的设想完全同构。但它不适合低资源服务器直接采用：Python ≥3.12 加约 40 个依赖部署沉重；语义搜索默认开启要下载百 MB ONNX 模型并占 CPU；AGPL-3.0 禁止抄码；move/并发写是历史 bug 高发区（#1152/#1214）；本地备份官方让用户自理。我们的路线应为：Go 单二进制、默认纯 FTS、向量检索做成可选小模型、内置 git 自动备份、以他们的观察/关系/permalink 语法为蓝本自研，避开其依赖与写入链路的坑。
