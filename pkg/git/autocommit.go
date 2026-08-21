package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Options 配置 AutoCommitter。
type Options struct {
	// Root 是记忆库根目录（memory/），必须是已存在的目录。
	Root string
	// Enabled 开启自动提交。默认关闭：RunDaily 直接返回，Commit 为空操作
	// （返回 committed=false，不创建仓库、不产生任何提交）。自动定时提交由
	// serve 的 -git-autocommit 装配（默认开启，见 design.md §10 决策 3）。
	Enabled bool
	// UserName / UserEmail 覆盖新仓库的本地身份默认值（仅写入本地 config）；
	// 为空时使用 DefaultUserName / DefaultUserEmail。
	UserName  string
	UserEmail string
	// Logf 接收运行日志（提交/跳过/gc 提示等）；nil 时静默。
	Logf func(format string, args ...any)
	// Runner 注入外部命令执行器（测试用）；nil 时用 exec.Command 参数数组。
	Runner Runner
}

// AutoCommitter 对 memory/ git 仓库执行自动提交。
//
// 装配方式（design.md §10 决策 3，2026-08-22 变更）：由编排方
// （cmd/zipper-agent-memoryd serve）在独立 goroutine 运行 [RunDaily]，
// 每日 hour 点触发一次 [Commit]；用户可随时用 `git-commit` 子命令主动
// 触发一次 [Commit]。watcher 去抖批次内不再直接提交——定时提交以整棵树为
// 对象（git add -A），幂等，与事件批次解耦，天然无反馈环。
//
// 语义：
//   - 默认关闭（Enabled=false）：RunDaily 直接返回，Commit 返回
//     committed=false，不创建仓库、无任何提交；
//   - 开启且仓库缺失时自动 git init + 本地身份（EnsureRepo），首次提交
//     会把既有文件一并纳入基线；
//   - 提交信息模板：chore(memory): auto-commit <RFC3339 时间戳>（commitMessage）；
//   - 无暂存变更跳过提交（空批次 / 仅 .git 内部事件，防反馈环），
//     Commit 返回 committed=false；
//   - git gc 仅日志提示一次（design.md R4），从不自动执行。
type AutoCommitter struct {
	root      string
	enabled   bool
	userName  string
	userEmail string
	logf      func(string, ...any)
	run       Runner
	now       func() time.Time // 注入时钟（测试用）；nil 时用 time.Now

	gcHintOnce sync.Once
}

// NewAutoCommitter 校验 Root 并构造 AutoCommitter。
func NewAutoCommitter(opts Options) (*AutoCommitter, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("git: autocommitter: empty root")
	}
	abs, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("git: resolve root %q: %w", opts.Root, err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("git: stat root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("git: root %q is not a directory", abs)
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	userName, userEmail := opts.UserName, opts.UserEmail
	if userName == "" {
		userName = DefaultUserName
	}
	if userEmail == "" {
		userEmail = DefaultUserEmail
	}
	return &AutoCommitter{
		root:      abs,
		enabled:   opts.Enabled,
		userName:  userName,
		userEmail: userEmail,
		logf:      logf,
		run:       opts.Runner,
	}, nil
}

// Commit 对 memory/ 仓库执行一次「git add -A + git commit（有暂存变更时）」，
// 返回是否实际提交（committed=false 表示关闭状态或无可提交变更）。
// 同步执行；调用方包括 RunDaily（每日定时）与 git-commit 子命令（主动触发）。
func (ac *AutoCommitter) Commit() (bool, error) {
	if ac == nil || !ac.enabled {
		return false, nil
	}
	run := ac.run
	if run == nil {
		run = defaultRunner
	}

	// 仓库缺失自动初始化（含本地身份）；已有仓库补全缺失身份，均幂等。
	if _, err := ensureRepo(ac.root, run, ac.userName, ac.userEmail, ac.logf); err != nil {
		return false, err
	}

	// git add -A：参数数组（编码规范 §5.4），以 memory/ 整棵树为对象。
	if _, err := run(ac.root, "git", "add", "-A"); err != nil {
		return false, fmt.Errorf("git: add -A: %w", err)
	}

	// 无暂存变更则跳过提交（空批次 / 仅 .git 内部事件引发的批次），
	// 避免空提交与「提交 → fsnotify 事件 → 提交」反馈环。
	out, err := run(ac.root, "git", "diff", "--cached", "--name-only")
	if err != nil {
		return false, fmt.Errorf("git: diff --cached --name-only: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		ac.logf("git: no staged changes, skip commit")
		return false, nil
	}

	msg := commitMessage(time.Now())
	if _, err := run(ac.root, "git", "commit", "-m", msg); err != nil {
		return false, fmt.Errorf("git: commit: %w", err)
	}
	ac.logf("git: committed %q", msg)

	// git gc 仅日志提示（design.md §9 R4：仓库随记忆增长膨胀），不自动执行。
	ac.gcHintOnce.Do(func() {
		ac.logf("git: hint: run 'git gc' periodically to keep the repository compact (design R4; never auto-executed)")
	})
	return true, nil
}

// RunDaily 按「每日 hour 点」触发一次 Commit()，直到 ctx 取消（优雅退出）。
// 定时语义与 cron "0 hour * * *" 一致：以 time.Timer 计算到下一个 hour 点
// 的等待（编码规范 §10：禁轮询，等待期间零 CPU，不占资源）；hour 点已过
// （含恰在 hour:00:00 的边界）则顺延至明日。Commit 失败仅记日志并顺延
// 次日重试，不终止循环（守护进程语义）；gc 提示仍由 Commit 保证仅一次。
//
// 关闭状态（Enabled=false）直接返回 nil，不启动任何定时器。
func (ac *AutoCommitter) RunDaily(ctx context.Context, hour int) error {
	if ac == nil || !ac.enabled {
		return nil
	}
	if hour < 0 || hour > 23 {
		return fmt.Errorf("git: autocommitter: daily hour %d out of range [0,23]", hour)
	}
	nowFn := ac.now
	if nowFn == nil {
		nowFn = time.Now
	}
	for {
		timer := time.NewTimer(nextDelay(nowFn(), hour))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		ac.logf("git: daily commit trigger (scheduled %02d:00)", hour)
		if _, err := ac.Commit(); err != nil {
			ac.logf("git: daily commit failed: %v", err)
		}
	}
}

// nextDelay 返回从 now 到下一个「每日 hour 点」的等待时长（now 所在时区）。
// 语义与 cron "0 hour * * *" 一致：今日 hour 点已过（含恰在 hour:00:00 的
// 边界，视为已过）则指向明日，否则指向今日。hour 由调用方保证在 [0,23]
// （RunDaily 已校验）。纯函数，供单元测试直接验证边界。
func nextDelay(now time.Time, hour int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(now)
}
