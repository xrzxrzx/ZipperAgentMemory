# Git 提交规范

## 1. 提交信息

Conventional Commits：

```
<type>(<scope>): <summary>

<body: 动机说明>
```

- `type`: `feat` `fix` `docs` `refactor` `test` `chore` `perf` `style`
- `scope`: `memory` `index` `mcp` `cli` `watch` `git` `migrate` `docs` `ci` 等

## 2. 提交时机

- **阶段成果**按阶段自动提交（Conventional Commits）；
- **不主动提交**场景：小改动、临时需求、无法确保用户满意时 —— **先询问用户再提交**；
- **提交分型铁律**：代码与文档分开提交——代码变更用 `feat:`/`fix:`/`test:`，文档变更用 `docs:`；
  禁止用 `git add -A` 一次吞入混类型变更（阶段 0 曾将代码误并入 docs 提交，教训记录在案）。
  协调者提交前须 `git status` 核对暂存文件，分类分批提交。

## 3. 铁律

- 禁止 `git rebase` / `force-push` 改写提交历史；
- 合并一律 Squash Merge；
- 提交前 `go vet ./...`、`go build ./...`、`go test ./...` 通过（涉及代码时）。

## 4. 分支策略

- `master` 为唯一长期分支；阶段开发可直接在 master 上小步提交；
- 大规模功能或实验：短命分支 + Squash Merge 回 master。
