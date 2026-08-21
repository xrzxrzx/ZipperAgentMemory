package git

import (
	"bytes"
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
	// Enabled 开启 autocommit。默认关闭：Commit 为空操作，
	// 既不创建仓库也不产生任何提交（由 -git-autocommit 显式开启）。
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

// AutoCommitter 对 memory/ git 仓库执行「变更去抖批次后自动提交」。
//
// 装配方式：由编排方（cmd/zipper-agent-memoryd serve）在 watcher 的
// 去抖批次 Handler 内同步调用 Commit()——与索引更新同一 goroutine，天然
// 串行，无并发 git 操作；git add -A 以整棵树为对象，幂等。
//
// 语义：
//   - 默认关闭：Enabled=false 时 Commit 直接返回，不创建仓库、无任何提交；
//   - 开启且仓库缺失时自动 git init + 本地身份（EnsureRepo），首个含变更的
//     批次会把既有文件一并纳入首次基线提交；
//   - 提交信息模板：chore(memory): auto-commit <RFC3339 时间戳>（commitMessage）；
//   - 无暂存变更的批次跳过提交（空批次 / 仅 .git 内部事件，防反馈环）；
//   - git gc 仅日志提示一次（design.md R4），从不自动执行。
type AutoCommitter struct {
	root      string
	enabled   bool
	userName  string
	userEmail string
	logf      func(string, ...any)
	run       Runner

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

// Commit 对 memory/ 仓库执行一次「git add -A + git commit（有暂存变更时）」。
// 关闭状态直接返回 nil。同步执行，由编排方在 watcher 去抖批次内调用。
func (ac *AutoCommitter) Commit() error {
	if ac == nil || !ac.enabled {
		return nil
	}
	run := ac.run
	if run == nil {
		run = defaultRunner
	}

	// 仓库缺失自动初始化（含本地身份）；已有仓库补全缺失身份，均幂等。
	if _, err := ensureRepo(ac.root, run, ac.userName, ac.userEmail, ac.logf); err != nil {
		return err
	}

	// git add -A：参数数组（编码规范 §5.4），以 memory/ 整棵树为对象。
	if _, err := run(ac.root, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git: add -A: %w", err)
	}

	// 无暂存变更则跳过提交（空批次 / 仅 .git 内部事件引发的批次），
	// 避免空提交与「提交 → fsnotify 事件 → 提交」反馈环。
	out, err := run(ac.root, "git", "diff", "--cached", "--name-only")
	if err != nil {
		return fmt.Errorf("git: diff --cached --name-only: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		ac.logf("git: no staged changes, skip commit")
		return nil
	}

	msg := commitMessage(time.Now())
	if _, err := run(ac.root, "git", "commit", "-m", msg); err != nil {
		return fmt.Errorf("git: commit: %w", err)
	}
	ac.logf("git: committed %q", msg)

	// git gc 仅日志提示（design.md §9 R4：仓库随记忆增长膨胀），不自动执行。
	ac.gcHintOnce.Do(func() {
		ac.logf("git: hint: run 'git gc' periodically to keep the repository compact (design R4; never auto-executed)")
	})
	return nil
}
