# ZipperAgentMemory

AI 跨平台永久性记忆工具：在 Linux 低资源服务器上运行的常驻进程，维护一个 **Markdown + CSV 文件型记忆库**，经 **MCP（stdio）** 供 Claude / Cursor / DSH 等 Agent 读写与检索；记忆库即普通目录树，支持 **目录复制** 与 **git** 双模式迁移。

## 设计文档

- 设计：`docs/design.md`（当前 v0.1 草案，待批准）
- 编码规范：`docs/standards/go-编码规范.md`
- 调研报告：`docs/research/`
- 项目记忆：`docs/memory/`

## 状态

> **已批准开工**（2026-08-21）—— 设计文档 v1.0 经用户批准生效，阶段 0（Go 模块骨架）进行中。

详见 `docs/memory/status.md`（项目状态快照）与 `docs/design.md`（唯一权威设计文档）。
