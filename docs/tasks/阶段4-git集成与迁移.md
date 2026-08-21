# 任务书（草稿）：阶段 4 —— git 集成与迁移

> 状态：**草稿，待设计文档批准后生效**。

## 任务书：阶段 4 git 集成与迁移脚本

- **派发者**：协调者
- **依据**：`docs/design.md` §7（迁移方案）、§8 阶段 4；`AGENTS.d/git.md`；`docs/memory/server-评估.md`（部署规划）
- **目标**：可选 autocommit + 迁移脚本，打通「目录复制 / git 双模式迁移」
- **前置**：阶段 2 已交付 watcher（事件流）
- **范围**：
  - `pkg/git`：
    - autocommit：监听 watcher 去抖后的事件流，对 memory/ 仓库执行 `git add -A && git commit`（参数数组调用 git 命令，禁用 shell 字符串）
    - **默认关闭**，由配置/flag（`--git-autocommit`）显式开启；开启时提交信息模板（如 `chore(memory): auto-commit <timestamp>`）
    - `git gc` 提示（日志级，不自动执行）
  - `scripts/migrate.sh`：
    - 打包：`tar`/`zip` 打包 memory/（含说明：目标机解压后 `--rebuild-index`）
    - git bundle：`git bundle create memory.bundle --all` 单文件迁移说明
    - 引导：新机初始化 memory/ git 仓库 + 重建索引
  - 配置：配置文件（TOML/JSON 或 flag，由编码 Agent 提出方案，协调者定）承载 autocommit 开关、memory 路径、端口等
  - **不做**：远程 git 推送/拉取自动化（v1 由用户手动 git clone/push/pull）
- **验收标准**：
  1. autocommit 关闭时文件变更**不**产生提交；
  2. autocommit 开启时变更去抖后自动生成提交（提交信息符合模板）；
  3. 迁移脚本在干净目录还原出可用记忆库（解压/克隆 → rebuild-index → 搜索可用）；
  4. git bundle 单文件迁移在干净目录 clone 成功；
  5. `go test ./...` 全绿。
- **交付物**：`pkg/git/*`（含测试）、`scripts/migrate.sh`、配置文件方案
- **约束**：git 调用全部参数数组、禁 shell 字符串（编码规范 §5.3）；autocommit 默认关闭是用户待拍板项，若用户改默认开则调整
- **DoD**：5 项验收通过且证据留档（`docs/验收/阶段4.md`）
