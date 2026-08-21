package git

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// runGitOK 在测试中直接调用系统 git（参数数组；仅操作 t.TempDir() 本地仓库，
// 无网络）。失败即终止测试。
func runGitOK(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := defaultRunner(dir, "git", args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// writeFile 创建 dir 下的文件（含父目录）。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestAutoCommitterEnabledFullFlow 真实 git 全流程（验收 2）：
// 开启时仓库缺失自动 init → 首次提交生成 → 提交信息符合模板 →
// 二次变更生成第二个提交 → 工作树干净。
func TestAutoCommitterEnabledFullFlow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes", "a.md"), "# A\nhello world\n")

	ac, err := NewAutoCommitter(Options{Root: dir, Enabled: true, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	committed, err := ac.Commit()
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	if !committed {
		t.Fatal("first Commit 应实际提交")
	}
	if !IsRepo(dir) {
		t.Fatal("repository was not auto-initialized")
	}
	// 本地身份已写入（不碰全局）。
	if got := strings.TrimSpace(runGitOK(t, dir, "config", "--local", "user.name")); got != DefaultUserName {
		t.Fatalf("local user.name = %q, want %q", got, DefaultUserName)
	}
	// 提交信息符合模板。
	msg := strings.TrimSpace(runGitOK(t, dir, "log", "--format=%s", "-1"))
	if !regexp.MustCompile(`^chore\(memory\): auto-commit `).MatchString(msg) {
		t.Fatalf("first commit message %q does not match template", msg)
	}

	// 第二次变更 → 第二个提交。
	writeFile(t, filepath.Join(dir, "notes", "b.md"), "# B\n")
	if _, err := ac.Commit(); err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if got := strings.TrimSpace(runGitOK(t, dir, "rev-list", "--count", "HEAD")); got != "2" {
		t.Fatalf("commit count = %s, want 2", got)
	}
	if got := runGitOK(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("working tree not clean: %q", got)
	}
}

// TestAutoCommitterDisabledNoOp 验收 1：关闭状态（默认）不产生任何提交，
// 且不初始化仓库；对已有仓库也不产生新提交。
func TestAutoCommitterDisabledNoOp(t *testing.T) {
	// 仓库不存在：关闭状态连 git init 都不执行。
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes", "a.md"), "# A\n")
	ac, err := NewAutoCommitter(Options{Root: dir, Enabled: false, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	committed, err := ac.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if committed {
		t.Fatal("disabled autocommit 不应返回 committed=true")
	}
	if IsRepo(dir) {
		t.Fatal("disabled autocommit must not initialize the repository")
	}

	// 已有仓库 + 已提交基线：关闭状态文件变更不产生新提交。
	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "notes", "a.md"), "# A\n")
	runGitOK(t, dir2, "init")
	runGitOK(t, dir2, "config", "--local", "user.name", "tester")
	runGitOK(t, dir2, "config", "--local", "user.email", "tester@example.com")
	runGitOK(t, dir2, "add", "-A")
	runGitOK(t, dir2, "commit", "-m", "baseline")
	writeFile(t, filepath.Join(dir2, "notes", "b.md"), "# B\n")

	ac2, err := NewAutoCommitter(Options{Root: dir2, Enabled: false, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	committed, err = ac2.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if committed {
		t.Fatal("disabled autocommit 不应返回 committed=true")
	}
	if got := strings.TrimSpace(runGitOK(t, dir2, "rev-list", "--count", "HEAD")); got != "1" {
		t.Fatalf("disabled autocommit created commits: HEAD count = %s, want 1", got)
	}
}

// TestAutoCommitterSkipsWhenNothingStaged 真实 git：无变更批次跳过提交
// （空仓库无提交；已有基线无变更不新增提交）。
func TestAutoCommitterSkipsWhenNothingStaged(t *testing.T) {
	// 空目录：add -A 无暂存 → 跳过提交，仓库仅 init。
	dir := t.TempDir()
	ac, err := NewAutoCommitter(Options{Root: dir, Enabled: true, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	committed, err := ac.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if committed {
		t.Fatal("空目录不应产生提交")
	}
	if !IsRepo(dir) {
		t.Fatal("repo should be initialized")
	}
	if _, err := defaultRunner(dir, "git", "rev-parse", "--verify", "HEAD"); err == nil {
		t.Fatal("expected no commits on empty dir")
	}

	// 已有基线 + 无变更：不新增提交。
	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "notes", "a.md"), "# A\n")
	ac2, err := NewAutoCommitter(Options{Root: dir2, Enabled: true, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	if _, err := ac2.Commit(); err != nil {
		t.Fatalf("baseline Commit: %v", err)
	}
	committed, err = ac2.Commit()
	if err != nil {
		t.Fatalf("no-change Commit: %v", err)
	}
	if committed {
		t.Fatal("无变更不应返回 committed=true")
	}
	if got := strings.TrimSpace(runGitOK(t, dir2, "rev-list", "--count", "HEAD")); got != "1" {
		t.Fatalf("no-change batch created a commit: HEAD count = %s, want 1", got)
	}
}

// TestEnsureRepoIdempotent 验收 3：git-init 幂等（跑两次不报错）。
func TestEnsureRepoIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureRepo(dir); err != nil {
		t.Fatalf("first EnsureRepo: %v", err)
	}
	if err := EnsureRepo(dir); err != nil {
		t.Fatalf("second EnsureRepo (idempotent): %v", err)
	}
	if !IsRepo(dir) {
		t.Fatal("repo not created")
	}
	if got := strings.TrimSpace(runGitOK(t, dir, "config", "--local", "user.name")); got != DefaultUserName {
		t.Fatalf("local user.name = %q, want %q", got, DefaultUserName)
	}
}

// TestEnsureRepoKeepsExistingIdentity 已有仓库的既有本地身份不被覆盖。
func TestEnsureRepoKeepsExistingIdentity(t *testing.T) {
	dir := t.TempDir()
	runGitOK(t, dir, "init")
	runGitOK(t, dir, "config", "--local", "user.name", "Alice")
	runGitOK(t, dir, "config", "--local", "user.email", "alice@example.com")
	if err := EnsureRepo(dir); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if got := strings.TrimSpace(runGitOK(t, dir, "config", "--local", "user.name")); got != "Alice" {
		t.Fatalf("existing identity overwritten: user.name = %q, want Alice", got)
	}
}

// TestNewAutoCommitterValidation 构造校验：空 root / 不存在目录 / 非目录。
func TestNewAutoCommitterValidation(t *testing.T) {
	if _, err := NewAutoCommitter(Options{}); err == nil {
		t.Fatal("empty root must be rejected")
	}
	if _, err := NewAutoCommitter(Options{Root: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("nonexistent root must be rejected")
	}
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAutoCommitter(Options{Root: f}); err == nil {
		t.Fatal("file root must be rejected")
	}
}

// TestNextDelay 验收 3：nextDelay 边界（0 点/跨天/恰在 hour 点/跨月/跨年）。
// 语义与 cron "0 hour * * *" 一致：今日 hour 点已过（含恰在 hour:00:00）
// 则顺延明日，否则指向今日。
func TestNextDelay(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		hour int
		want time.Duration
	}{
		{"今日未到 0 点（10:30 → 明日 0 点）", time.Date(2026, 8, 22, 10, 30, 0, 0, time.UTC), 0, 13*time.Hour + 30*time.Minute},
		{"今日已过 0 点（23:59:59 → 明日 0 点，1 秒后）", time.Date(2026, 8, 22, 23, 59, 59, 0, time.UTC), 0, time.Second},
		{"恰在 0 点整（边界：视为已过 → 明日 0 点，24 小时）", time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC), 0, 24 * time.Hour},
		{"0 点过 1ms → 明日 0 点", time.Date(2026, 8, 22, 0, 0, 0, 1_000_000, time.UTC), 0, 24*time.Hour - time.Millisecond},
		{"恰在 hour 点（12:00 整 → 明日 12:00，24 小时）", time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC), 12, 24 * time.Hour},
		{"hour 点前 1 秒（11:59:59 → 今日 12:00，1 秒后）", time.Date(2026, 8, 22, 11, 59, 59, 0, time.UTC), 12, time.Second},
		{"跨月（8-31 23:00 → 9-1 0:00）", time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC), 0, time.Hour},
		{"跨年（12-31 23:30 → 1-1 0:00）", time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC), 0, 30 * time.Minute},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextDelay(tt.now, tt.hour); got != tt.want {
				t.Errorf("nextDelay(%v, %d) = %v, want %v", tt.now, tt.hour, got, tt.want)
			}
		})
	}
	// 不变式：任意整点 hour 与任意时刻，等待时长恒为正（严格指向未来）。
	for hour := 0; hour < 24; hour++ {
		for _, now := range []time.Time{
			time.Date(2026, 8, 22, hour, 0, 0, 0, time.UTC),
			time.Date(2026, 8, 22, hour, 30, 0, 0, time.UTC),
			time.Date(2026, 8, 22, 23, 59, 59, 0, time.UTC),
		} {
			if d := nextDelay(now, hour); d <= 0 {
				t.Errorf("nextDelay(%v, %d) = %v, want > 0", now, hour, d)
			}
		}
	}
}

// TestRunDailyDisabledNoOp 验收：Enabled=false 时 RunDaily 直接返回，
// 不启动定时器、不产生任何 git 调用（Enabled 语义不变，决策 3）。
func TestRunDailyDisabledNoOp(t *testing.T) {
	rec := &recordingRunner{}
	ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: false, Runner: rec.run, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	start := time.Now()
	if err := ac.RunDaily(context.Background(), 0); err != nil {
		t.Fatalf("RunDaily: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("禁用状态 RunDaily 应立即返回，实际 %v", elapsed)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("禁用状态不应有任何 git 调用：%v", rec.calls)
	}
}

// TestRunDailyInvalidHour 验收：hour 越界（<0 或 >23）返回错误。
func TestRunDailyInvalidHour(t *testing.T) {
	ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	for _, hour := range []int{-1, 24, 25} {
		if err := ac.RunDaily(context.Background(), hour); err == nil {
			t.Errorf("hour=%d 应返回错误", hour)
		}
	}
}

// TestRunDailyCancelExit 验收：ctx 取消后 RunDaily 优雅退出（不等到下一个
// hour 点），且不触发任何提交。
func TestRunDailyCancelExit(t *testing.T) {
	rec := &recordingRunner{}
	ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: true, Runner: rec.run, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消的 ctx
	start := time.Now()
	if err := ac.RunDaily(ctx, 0); err != nil {
		t.Fatalf("RunDaily: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("RunDaily 未在 ctx 取消后快速返回，实际 %v", elapsed)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("ctx 已取消不应触发任何 git 调用：%v", rec.calls)
	}
}

// TestRunDailyFiresOnSchedule 验收 2：RunDaily 到达下一个 hour 点（0 点）
// 时触发一次 Commit，随后顺延次日继续等待；ctx 取消时优雅退出。
// 不真等一天：注入伪时钟，首次取 23:59:59.999（→ 0 点，等待 ~1ms 即触发），
// 此后取次日 00:00:30（→ 下一个 0 点，等待约一天）。
func TestRunDailyFiresOnSchedule(t *testing.T) {
	rec := &recordingRunner{staged: true, diff: []byte("notes/a.md\n")}
	ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: true, Runner: rec.run, Logf: t.Logf})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	var calls atomic.Int32
	ac.now = func() time.Time {
		if calls.Add(1) == 1 {
			return time.Date(2026, 8, 22, 23, 59, 59, 999_000_000, time.Local)
		}
		return time.Date(2026, 8, 23, 0, 0, 30, 0, time.Local)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ac.RunDaily(ctx, 0) }()
	defer cancel()

	// 首次触发（等待 ~1ms）：轮询直到记录到 git commit 调用。
	waitFor(t, 5*time.Second, func() bool { return hasGitCall(rec, "commit") })
	// 提交完成后 RunDaily 立即进入第二轮调度（now 第二次调用），
	// 此时已创建「下一个 0 点」的长定时器。
	waitFor(t, 5*time.Second, func() bool { return calls.Load() == 2 })

	// 取消 ctx：RunDaily 应优雅退出（不真等一天）。
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDaily: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunDaily 未在 ctx 取消后退出（goroutine 泄漏）")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("now 调用次数 = %d, want 2（首次触发 + 顺延次日各一次）", got)
	}
	if got := countGitCalls(rec, "commit"); got != 1 {
		t.Fatalf("commit 调用次数 = %d, want 1（每日仅一次）", got)
	}
}

// hasGitCall 报告 recordingRunner 是否记录过 git <sub> 调用。
func hasGitCall(rec *recordingRunner, sub string) bool {
	return countGitCalls(rec, sub) > 0
}

// countGitCalls 统计 recordingRunner 记录过的 git <sub> 调用次数。
func countGitCalls(rec *recordingRunner, sub string) int {
	n := 0
	for _, c := range rec.calls {
		if len(c) >= 2 && c[0] == "git" && c[1] == sub {
			n++
		}
	}
	return n
}

// waitFor 轮询 cond 直到为真或超时（测试辅助，短间隔小步进）。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待条件超时（%v）", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
