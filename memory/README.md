# 记忆库（memory/）

ZipperAgentMemory 的持久化记忆目录；整个目录即未来的 git 仓库（阶段 4 可选 autocommit）。

## 目录结构（Schema v1，见 docs/design.md §5）

```
memory/
├── README.md                ← 本说明（人读入口）
├── index.md                 ← 总索引：目录导航 + 最近更新（阶段 2 起自动维护）
├── notes/                   ← 通用笔记：每主题一个 .md（文件名=主题，蛇形命名）
├── projects/                ← 项目知识：每项目一个子目录
│   └── <project>/README.md  ← 项目概况
├── structured/              ← 结构化数据（CSV / Markdown 表格，同主题二选一）
├── agent/                   ← Agent 自动沉淀：agent/<agent-id>/YYYY-MM.md 按月归档
└── meta/schema-version      ← 记忆库 schema 版本号
```

## 约定

- 文件正文用 Markdown；文件名蛇形命名，禁止空格与中文文件名（内容可以中文）
- 文件首行（CSV 为表头）作为内容摘要来源，供 FTS 与索引使用
- 每文件前几行可含 YAML front-matter（`tags` / `created` / `source`）
- agent/ 目录只通过 `zam append`（memory_append）写入，格式由工具强约束
