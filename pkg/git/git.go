package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 本地仓库身份默认值（仅写入 memory/ 仓库的本地 config，不碰全局；
// 用户可在 memory/ 内自行 git config 覆盖，本包不覆盖既有本地身份）。
const (
	DefaultUserName  = "zipper-agent-memory"
	DefaultUserEmail = "zipper-agent-memory@localhost"
)

// Runner 执行外部命令（dir 为工作目录），返回合并输出与错误。
// 生产环境为 defaultRunner（exec.Command 参数数组，编码规范 §5.4）；
// 测试可注入记录/伪造实现以验证命令构造。
type Runner func(dir, name string, args ...string) ([]byte, error)

// CmdError 表示外部命令非零退出。Output 为合并输出（stdout+stderr）；
// ExitCode<0 表示无法获取退出码（如命令未找到）。
type CmdError struct {
	Dir      string
	Name     string
	Args     []string
	ExitCode int
	Output   string
	Err      error
}

func (e *CmdError) Error() string {
	return fmt.Sprintf("git: %s %s in %s exited with code %d: %s",
		e.Name, strings.Join(e.Args, " "), e.Dir, e.ExitCode, strings.TrimSpace(e.Output))
}

// Unwrap 保留底层错误链（编码规范 §3.2）。
func (e *CmdError) Unwrap() error { return e.Err }

// defaultRunner 用 exec.Command 以参数数组调用外部命令
// （编码规范 §5.4：git 等外部命令必须参数数组传参，禁用 shell 字符串）。
func defaultRunner(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, nil
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	return out, &CmdError{Dir: dir, Name: name, Args: args, ExitCode: code, Output: string(out), Err: err}
}

// commitMessage 生成 autocommit 提交信息模板：
// chore(memory): auto-commit <RFC3339 时间戳>（UTC，机器可排序）。
func commitMessage(now time.Time) string {
	return "chore(memory): auto-commit " + now.UTC().Format(time.RFC3339)
}

// IsRepo 报告 root 是否已是 git 仓库（存在 .git 目录）。
func IsRepo(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// EnsureRepo 确保 root 为已初始化的 git 仓库并具备本地 user.name/email
// （幂等，git-init 子命令与 AutoCommitter 自动初始化共用）：
//   - 仓库不存在：git init + 写入默认本地身份；
//   - 仓库已存在：仅当本地缺失 user.name/email 时补默认值，不覆盖既有身份。
//
// 全程只写本地 config（git config --local），不触碰全局配置。
func EnsureRepo(root string) error {
	_, err := ensureRepo(root, defaultRunner, DefaultUserName, DefaultUserEmail, nil)
	return err
}

// ensureRepo 是 EnsureRepo 的可注入实现（run/logf 供测试使用，logf 为 nil 时静默）。
// 返回是否新建了仓库。
func ensureRepo(root string, run Runner, userName, userEmail string, logf func(string, ...any)) (bool, error) {
	if run == nil {
		run = defaultRunner
	}
	if !IsRepo(root) {
		if _, err := run(root, "git", "init"); err != nil {
			return false, fmt.Errorf("git: init %s: %w", root, err)
		}
		if logf != nil {
			logf("git: initialized repository at %s", root)
		}
		if err := ensureIdentity(root, run, userName, userEmail); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := ensureIdentity(root, run, userName, userEmail); err != nil {
		return false, err
	}
	return false, nil
}

// ensureIdentity 保证仓库具备本地 user.name/email：
// 缺失（本地 config 未设置）时写入默认值；已设置则原样保留（幂等、不覆盖）。
func ensureIdentity(root string, run Runner, userName, userEmail string) error {
	for _, kv := range []struct{ key, value string }{
		{"user.name", userName},
		{"user.email", userEmail},
	} {
		cur, err := getLocalConfig(run, root, kv.key)
		if err != nil {
			return err
		}
		if cur != "" {
			continue // 已有本地身份，不覆盖
		}
		if _, err := run(root, "git", "config", "--local", kv.key, kv.value); err != nil {
			return fmt.Errorf("git: config --local %s: %w", kv.key, err)
		}
	}
	return nil
}

// getLocalConfig 读取仓库本地 config 的 key 值；本地未设置时返回 ("", nil)
// （git config --local --get 未命中退出码为 1，视为「未设置」而非错误）。
func getLocalConfig(run Runner, root, key string) (string, error) {
	out, err := run(root, "git", "config", "--local", "--get", key)
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	var ce *CmdError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return "", nil
	}
	return "", fmt.Errorf("git: config --local --get %s: %w", key, err)
}
