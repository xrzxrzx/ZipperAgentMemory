# ZipperAgentMemory 记忆仓库（docs/memory/）

> 本目录是项目的**记忆仓库**：跨会话、跨 Agent 恢复上下文的唯一权威来源。
> 规则：任何重要决策、状态变更、阶段结论，必须在此留痕，然后才谈得上「记忆」。

## 如何读写本仓库

- 新会话/新 Agent 开工前：先读 `status.md` 与最新的 `decisions/*.md`；
- 每次阶段结束：更新 `status.md`；有决策则追加 `decisions/`；
- 文件命名：`YYYY-MM-DD-主题.md`。

## 当前索引

| 文件 | 内容 |
|------|------|
| `status.md` | 项目当前状态快照（阶段、决策、开放问题、资源红线） |
| `decisions/` | 决策记录（ADR 风格） |
