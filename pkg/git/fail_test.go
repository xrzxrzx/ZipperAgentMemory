package git

import (
	"errors"
	"strings"
	"testing"
)

// stubRunner 可按子命令注入失败（args[0] 命中 fail 即返回 CmdError），
// 其余命令返回成功；diffOut 模拟 git diff --cached --name-only 的输出。
type stubRunner struct {
	fail    map[string]bool // 键为子命令名（如 "init"/"add"/"diff"/"commit"/"config"）
	diffOut []byte          // diff 命令的模拟输出（staged 文件列表）
	calls   [][]string
}

func (r *stubRunner) run(dir, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	if r.fail[sub] {
		return nil, &CmdError{
			Dir: dir, Name: name, Args: args, ExitCode: 128,
			Output: "injected failure", Err: errors.New("injected"),
		}
	}
	if sub == "diff" {
		return r.diffOut, nil
	}
	return nil, nil
}

// TestCommitErrorPropagation 失败路径：git init/add/diff/commit 任一失败，
// Commit 都返回带上下文包装的错误，且错误链可 errors.As 到 *CmdError。
func TestCommitErrorPropagation(t *testing.T) {
	cases := []struct {
		name string
		fail map[string]bool
		want string // 错误信息应包含的片段
	}{
		{"init 失败", map[string]bool{"init": true}, "git: init"},
		{"add 失败", map[string]bool{"add": true}, "git: add -A"},
		{"diff 失败", map[string]bool{"diff": true}, "git: diff --cached --name-only"},
		{"commit 失败", map[string]bool{"commit": true}, "git: commit"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := &stubRunner{fail: tt.fail, diffOut: []byte("notes/a.md\n")}
			ac, err := NewAutoCommitter(Options{Root: t.TempDir(), Enabled: true, Runner: r.run})
			if err != nil {
				t.Fatal(err)
			}
			_, err = ac.Commit()
			if err == nil {
				t.Fatal("Commit 应失败")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("错误 %q 应包含 %q", err, tt.want)
			}
			var ce *CmdError
			if !errors.As(err, &ce) {
				t.Fatalf("错误链应可 errors.As 到 *CmdError：%v", err)
			}
			if ce.ExitCode != 128 {
				t.Errorf("CmdError.ExitCode = %d, want 128", ce.ExitCode)
			}
		})
	}
}

// TestGetLocalConfigNonOneExitIsError 边界：git config --get 退出码非 0/1
// 时视为错误（只有退出码 1 表示「本地未设置」）。
func TestGetLocalConfigNonOneExitIsError(t *testing.T) {
	r := &stubRunner{fail: map[string]bool{"config": true}}
	if _, err := getLocalConfig(r.run, t.TempDir(), "user.name"); err == nil {
		t.Fatal("退出码 128 应报错而非视为未设置")
	}
}

// TestCmdErrorUnwrap 错误链：CmdError 可解包到底层 error（errors.Is 可用）。
func TestCmdErrorUnwrap(t *testing.T) {
	base := errors.New("boom")
	ce := &CmdError{ExitCode: 1, Err: base}
	if !errors.Is(ce, base) {
		t.Error("CmdError 应解包到底层错误")
	}
	if ce.Error() == "" {
		t.Error("CmdError.Error() 不应为空")
	}
}

// TestDefaultRunnerNotFound 失败路径：git 二进制缺失时返回 CmdError 且
// ExitCode<0（无法获取退出码）。仅验证错误形态，不依赖系统 git。
func TestDefaultRunnerNotFound(t *testing.T) {
	_, err := defaultRunner(t.TempDir(), "zipper-agent-memory-no-such-binary")
	if err == nil {
		t.Fatal("不存在的命令应报错")
	}
	var ce *CmdError
	if !errors.As(err, &ce) {
		t.Fatalf("应为 *CmdError：%v", err)
	}
	if ce.ExitCode >= 0 {
		t.Errorf("ExitCode = %d, want <0（命令未找到）", ce.ExitCode)
	}
}
