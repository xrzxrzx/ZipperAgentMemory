# 调研交叉验证笔记（协调者抽查）

> 日期：2026-02-13
> 来源：basic-memory 仓库一手材料（README.md、AGENTS.md，位于 `.research-cache/basic-memory/`）
> 用途：协调者抽查验证后台调研子 Agent 的调研方向与结论质量；子 Agent 报告交付后与此对照。

## 直接确认的事实

1. **文件即真源**：README「Local-first. Plain text on your disk. Forever.」；AGENTS.md「SQLite is used for indexing and full text search, files are source of truth」——与我们设计一致（索引是缓存，文件是真相源）。
2. **canonical vs derived state**：AGENTS.md 明确区分 canonical state（markdown 字节）与 derived state（索引/图谱行），derived 是 eventually consistent by design，且**明确禁止**为 derived 短暂不一致加锁/加事务——对我们「索引可随时重建」的决策是强佐证。
3. **MCP 有 tools 也有 prompts**：`continue_conversation`、`recent_activity`、`search`、`ai_assistant_guide` 等 prompts 辅助 agent 正确用工具——我们的 MCP 接口可在 v1 后补 prompts。
4. **语义搜索是 optional 且重**：向量 + cross-encoder reranking 需 Postgres/Milvus 或 SQLite 向量扩展——不适合低资源服务器，印证我们不引入向量的决策。
5. **许可证：AGPL-3.0**（README 徽章）——强 copyleft，**只能借鉴设计思想，严禁复制代码**，否则许可证传染。
6. **SQLite WAL**：AGENTS.md「WAL mode for SQLite performance」——写入我们的编码规范/设计（并发读写）。
7. **运行形态**：Python 3.12+，`uv tool install basic-memory`；本地模式 SQLite + in-process ASGI。它是有 API 层的（FastAPI），我们更轻——只有 CLI + MCP，不需要 HTTP API。
8. **知识图谱是它的差异化核心**（Observations `- [category]`、Relations `[[WikiLinks]]`、front-matter）——我们的 v1 不做图谱，保持文件直读直写。

## 对我们的借鉴/回避清单

| 项 | 借鉴 | 回避 |
|----|------|------|
| canonical/derived 分层 | ✅ 写进设计（索引=derived） | |
| SQLite WAL | ✅ 写进规范 | |
| MCP prompts | ⏳ v1 后补 | |
| 知识图谱 | | ✅ v1 不做 |
| 语义搜索/向量 | | ✅ 不做（资源） |
| FastAPI/HTTP API 层 | | ✅ 不引入（CLI+MCP 足够） |
| AGPL 代码 | | ✅ 严禁复制 |

## 待子 Agent 报告补齐的盲区

- 资源占用实测数字（内存/CPU）
- 已知问题与高频抱怨（GitHub issues）
- MCP Go SDK 选型结论（另一个子 Agent）
