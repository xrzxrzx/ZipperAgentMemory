// Package git 实现记忆库的可选 git 集成（阶段 4）。
//
// 能力：
//   - AutoCommitter：对 memory/ git 仓库执行「变更去抖批次后自动提交」
//     （git add -A + git commit），默认关闭、由开关显式开启；
//   - EnsureRepo：幂等初始化 memory/ 为 git 仓库并补全本地 user.name/email
//     （git-init 子命令与 autocommit 自动初始化共用）。
//
// 设计约束（编码规范 §5.4 安全红线）：
//   - 全部 git 调用经 exec.Command 以参数数组执行，禁止 shell 字符串拼接；
//     Runner 可注入，供测试记录/伪造调用以验证命令构造；
//   - 身份配置仅写仓库本地 config（git config --local），绝不触碰全局；
//   - git gc 仅日志提示（design.md §9 R4），从不自动执行；
//   - 仓库缺失时 autocommit 自动 git init（首个含变更的批次同时产生基线
//     提交）；已有仓库的既有本地身份不被覆盖。
//
// 本包不依赖 pkg/watch：去抖由 watcher 完成，编排方（cmd serve）在去抖
// 批次 Handler 内同步调用 AutoCommitter.Commit——与索引更新同一 goroutine，
// 天然串行，无并发 git 操作。
package git
