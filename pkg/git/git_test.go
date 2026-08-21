package git

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// recordingRunner 记录每次命令调用，并模拟 git 的「本地身份未设置」与
// 「暂存区内容」行为，供验证命令构造（参数数组、无 shell 拼接）。
type recordingRunner struct {
	dirs   []string
	calls  [][]string // 每项首元素为命令名，其后为参数
	diff   []byte     // git diff --cached --name-only 的模拟输出
	staged bool       // true=模拟有暂存变更
}

func (r *recordingRunner) run(dir, name string, args ...string) ([]byte, error) {
	r.dirs = append(r.dirs, dir)
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)

	// git config --local --get <key>：模拟本地未设置（退出码 1 → 视为未设置）。
	if len(args) == 3 && args[0] == "config" && args[1] == "--local" && args[2] == "--get" {
		return nil, &CmdError{Dir: dir, Name: name, Args: args, ExitCode: 1}
	}
	// git diff --cached --name-only：按 staged 标记返回暂存文件列表。
	if len(args) == 3 && args[0] == "diff" && args[1] == "--cached" && args[2] == "--name-only" {
		if r.staged {
			return r.diff, nil
		}
		return nil, nil
	}
	return nil, nil
}

// assertAllCallsNoShell 校验全部调用均为参数数组：命令名固定为 git、
// 无 shell 元字符参数、不触碰全局配置。
func (r *recordingRunner) assertAllCallsNoShell(t *testing.T, wantDirs []string) {
	t.Helper()
	for i, call := range r.calls {
		if call[0] != "git" {
			t.Fatalf("call %d: expected command 'git', got %q", i, call[0])
		}
		if len(wantDirs) > 0 && r.dirs[i] != wantDirs[i] {
			t.Fatalf("call %d: dir = %q, want %q", i, r.dirs[i], wantDirs[i])
		}
		for _, a := range call[1:] {
			if a == "--global" {
				t.Fatalf("call %d: must not touch global config, got %q", i, call)
			}
			if strings.ContainsAny(a, ";&|<>$`\n") {
				t.Fatalf("call %d: shell metacharacter in argument %q (call %q)", i, a, call)
			}
		}
	}
}

// TestCommitUsesArgumentArrays 验证开启状态下一次提交的完整命令序列：
// 全部为参数数组（git <arg>...），无 shell 字符串拼接；身份仅写本地 config。
func TestCommitUsesArgumentArrays(t *testing.T) {
	rec := &recordingRunner{staged: true, diff: []byte("notes/a.md\n")}
	ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: true, Runner: rec.run})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	if err := ac.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	want := [][]string{
		{"git", "init"}, // 仓库缺失 → 自动 git init
		{"git", "config", "--local", "--get", "user.name"}, // 读本地身份（未设置）
		{"git", "config", "--local", "user.name", DefaultUserName},
		{"git", "config", "--local", "--get", "user.email"},
		{"git", "config", "--local", "user.email", DefaultUserEmail},
		{"git", "add", "-A"},
		{"git", "diff", "--cached", "--name-only"},
		{"git", "commit", "-m", ""}, // 第 9 个参数为提交信息，单独校验模板
	}
	if len(rec.calls) != len(want) {
		t.Fatalf("got %d calls, want %d:\n%v", len(rec.calls), len(want), rec.calls)
	}
	for i := range want {
		got, w := rec.calls[i], want[i]
		if len(got) != len(w) {
			t.Fatalf("call %d: %v, want shape %v", i, got, w)
		}
		for j := range w {
			if j == len(w)-1 && i == len(want)-1 {
				continue // 提交信息参数单独校验
			}
			if got[j] != w[j] {
				t.Fatalf("call %d arg %d = %q, want %q (call %v)", i, j, got[j], w[j], got)
			}
		}
	}
	// 提交信息符合模板。
	msg := rec.calls[len(want)-1][3]
	if !regexp.MustCompile(`^chore\(memory\): auto-commit \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(msg) {
		t.Fatalf("commit message %q does not match template", msg)
	}
	rec.assertAllCallsNoShell(t, nil)
}

// TestCommitSkipsWhenNoStagedChanges 验证无暂存变更时跳过提交（空批次/
// 仅 .git 内部事件防反馈环），但 init/add/diff 仍按序执行。
func TestCommitSkipsWhenNoStagedChanges(t *testing.T) {
	rec := &recordingRunner{staged: false}
	ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: true, Runner: rec.run})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	if err := ac.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	for _, call := range rec.calls {
		if len(call) >= 2 && call[1] == "commit" {
			t.Fatalf("commit must not run when nothing staged: %v", rec.calls)
		}
	}
	rec.assertAllCallsNoShell(t, nil)
}

// TestDisabledCommitIssuesNoCommands 验证关闭状态（默认）Commit 为空操作：
// 不初始化仓库、不产生任何 git 调用。
func TestDisabledCommitIssuesNoCommands(t *testing.T) {
	rec := &recordingRunner{}
	ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: false, Runner: rec.run})
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	if err := ac.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("disabled autocommit issued %d git commands: %v", len(rec.calls), rec.calls)
	}
}

// TestCommitMessageTemplate 验证提交信息模板：
// chore(memory): auto-commit <RFC3339 时间戳>。
func TestCommitMessageTemplate(t *testing.T) {
	got := commitMessage(time.Date(2026, 8, 22, 3, 4, 5, 0, time.UTC))
	want := "chore(memory): auto-commit 2026-08-22T03:04:05Z"
	if got != want {
		t.Fatalf("commitMessage = %q, want %q", got, want)
	}
	re := regexp.MustCompile(`^chore\(memory\): auto-commit \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$`)
	if !re.MatchString(commitMessage(time.Now())) {
		t.Fatalf("commitMessage(now) does not match template")
	}
}
