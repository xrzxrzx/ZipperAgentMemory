// Package git 实现记忆库的可选 git 集成（阶段 4 + 决策 3 变更）。
//
// 能力：
//   - AutoCommitter：对 memory/ git 仓库自动提交（git add -A + git commit）。
//     两种触发方式（design.md §10 决策 3，2026-08-22）：
//   - RunDaily(ctx, hour)：每日 hour 点定时提交一次（time.Timer，零轮询），
//     由 serve 装配（-git-autocommit 默认开启，每日 0 点）；
//   - Commit()：立即提交一次，由 git-commit 子命令提供用户主动触发入口；
//   - EnsureRepo：幂等初始化 memory/ 为 git 仓库并补全本地 user.name/email
//     （git-init 子命令与 autocommit 自动初始化共用）。
//
// 设计约束（编码规范 §5.4 安全红线）：
//   - 全部 git 调用经 exec.Command 以参数数组执行，禁止 shell 字符串拼接；
//     Runner 可注入，供测试记录/伪造调用以验证命令构造；
//   - 身份配置仅写仓库本地 config（git config --local），绝不触碰全局；
//   - git gc 仅日志提示（design.md §9 R4），从不自动执行；
//   - 仓库缺失时 autocommit 自动 git init（首次提交同时产生基线提交）；
//     已有仓库的既有本地身份不被覆盖。
//
// 本包不依赖 pkg/watch：定时提交以整棵树为对象（git add -A），幂等，
// 与事件批次解耦；编排方（cmd serve）在独立 goroutine 运行 RunDaily。
package git
