package git

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	if err := ac.Commit(); err != nil {
		t.Fatalf("first Commit: %v", err)
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
	if err := ac.Commit(); err != nil {
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
	if err := ac.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
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
	if err := ac2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
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
	if err := ac.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
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
	if err := ac2.Commit(); err != nil {
		t.Fatalf("baseline Commit: %v", err)
	}
	if err := ac2.Commit(); err != nil {
		t.Fatalf("no-change Commit: %v", err)
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
