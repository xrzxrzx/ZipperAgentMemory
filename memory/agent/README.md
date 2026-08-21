# agent/ Agent 自动沉淀

每 Agent 独立目录，避免互相污染：

```
agent/<agent-id>/YYYY-MM.md   ← 按月归档，仅通过 memory_append 写入
```

- 格式由工具强约束（自动时间戳分隔行），不直接手工编辑（design.md §9 R3）
