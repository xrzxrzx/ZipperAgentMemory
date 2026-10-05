package main

import (
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"zipper-agent-memory/pkg/git"
)

// TestServeFlagsAutocommitDefaultTrue 验收：serve 的 -git-autocommit 默认开启
// （design.md §10 决策 3：autocommit 默认开启，每日 0 点定时提交）。
func TestServeFlagsAutocommitDefaultTrue(t *testing.T) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	sf := registerServeFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("解析默认 flag: %v", err)
	}
	if !sf.gitAuto {
		t.Fatal("-git-autocommit 必须默认开启（决策 3）")
	}
	if sf.allowIPs != "" {
		t.Fatalf("-allow-ips 默认应为空（不限制），实际 %q", sf.allowIPs)
	}
	if sf.addr != "127.0.0.1:8931" {
		t.Fatalf("-addr 默认应保持 127.0.0.1:8931，实际 %q", sf.addr)
	}
	if sf.config != "" {
		t.Fatalf("-config 默认应为空，实际 %q", sf.config)
	}
	if sf.gitAutoHour != 0 {
		t.Fatalf("-git-autocommit-hour 默认应为 0，实际 %d", sf.gitAutoHour)
	}
}

// TestNewServeAutoCommitterWiring 验收：装配逻辑——开启时构造 AutoCommitter
// （serve 将启动每日定时 goroutine），关闭时不构造（不启动定时器）。
func TestNewServeAutoCommitterWiring(t *testing.T) {
	dir := t.TempDir()
	ac, err := newServeAutoCommitter(dir, true)
	if err != nil {
		t.Fatalf("newServeAutoCommitter(enabled): %v", err)
	}
	if ac == nil {
		t.Fatal("-git-autocommit=true 应构造 AutoCommitter")
	}
	ac2, err := newServeAutoCommitter(dir, false)
	if err != nil {
		t.Fatalf("newServeAutoCommitter(disabled): %v", err)
	}
	if ac2 != nil {
		t.Fatal("-git-autocommit=false 不应构造 AutoCommitter")
	}
	// 构造校验：root 不存在应报错（快速失败）。
	if _, err := newServeAutoCommitter(filepath.Join(t.TempDir(), "nope"), true); err == nil {
		t.Fatal("不存在的 root 应报错")
	}
}

// TestParseIPList 验收：-allow-ips 逗号分隔解析（去空白、去空项、空串 → 空）。
func TestParseIPList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"1.2.3.4", []string{"1.2.3.4"}},
		{"1.2.3.4,5.6.7.8", []string{"1.2.3.4", "5.6.7.8"}},
		{" 1.2.3.4 , 5.6.7.8 ,", []string{"1.2.3.4", "5.6.7.8"}},
		{",,", nil},
	}
	for _, tt := range cases {
		got := parseIPList(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("parseIPList(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseIPList(%q) = %v, want %v", tt.in, got, tt.want)
				break
			}
		}
	}
}

// TestCmdGitCommitRealRepo 验收：git-commit 子命令对临时仓库实测——
// 有变更 → 提交成功；无变更 → 跳过（不新增提交）。仓库缺失时自动 init。
func TestCmdGitCommitRealRepo(t *testing.T) {
	dir := t.TempDir()
	writeCmdTestFile(t, filepath.Join(dir, "notes", "a.md"), "# A\n")

	// 第一次：有变更 → 提交成功。
	out := captureStdout(t, func() {
		if err := cmdGitCommit([]string{"-root", dir}); err != nil {
			t.Fatalf("git-commit 首次: %v", err)
		}
	})
	if !strings.Contains(out, "committed changes") {
		t.Errorf("首次输出应含 committed changes，实际 %q", out)
	}
	if !git.IsRepo(dir) {
		t.Fatal("git-commit 应自动初始化仓库")
	}
	if got := cmdGitCount(t, dir); got != "1" {
		t.Fatalf("首次提交后 commit 数 = %s, want 1", got)
	}

	// 第二次：无变更 → 跳过（不新增提交）。
	out = captureStdout(t, func() {
		if err := cmdGitCommit([]string{"-root", dir}); err != nil {
			t.Fatalf("git-commit 二次: %v", err)
		}
	})
	if !strings.Contains(out, "no changes to commit") {
		t.Errorf("二次输出应含 no changes to commit，实际 %q", out)
	}
	if got := cmdGitCount(t, dir); got != "1" {
		t.Fatalf("无变更不应新增提交，commit 数 = %s, want 1", got)
	}
}

// writeCmdTestFile 创建测试文件（含父目录）。
func writeCmdTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cmdGitCount 返回仓库 HEAD 提交数（参数数组调用系统 git，仅操作 t.TempDir()）。
func cmdGitCount(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list --count HEAD: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// captureStdout 在 fn 执行期间捕获 os.Stdout 输出（命令式输出断言用）。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

// TestServeFlagsWebAddrDefault 验收：-web-addr 默认空串（= 不启用阅读页，
// 未配置时 serve 行为与旧版完全一致）。
func TestServeFlagsWebAddrDefault(t *testing.T) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	sf := registerServeFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("解析默认 flag: %v", err)
	}
	if sf.webAddr != "" {
		t.Fatalf("-web-addr 默认应为空（不启用阅读页），实际 %q", sf.webAddr)
	}
}

// TestCmdWebToken 验收：口令子命令——-init 生成且只打印长度、无 -init 打印
// 当前口令、已存在时拒绝覆盖、-force 完成轮换、缺失文件报错。
func TestCmdWebToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web-token")

	out := captureStdout(t, func() {
		if err := cmdWebToken([]string{"-token-file", path, "-init"}); err != nil {
			t.Fatalf("web-token -init: %v", err)
		}
	})
	if !strings.Contains(out, "wrote") {
		t.Errorf("-init 输出应说明已写入，实际 %q", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) != 43 {
		t.Errorf("口令长度 = %d，want 43（32 字节 base64url）", len(token))
	}
	if strings.Contains(out, token) {
		t.Error("-init 不应把口令本身打印到屏幕")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("口令文件权限 = %#o，want 0600", perm)
		}
	}

	// 无 -init：打印当前口令（供人工取用）。
	out = captureStdout(t, func() {
		if err := cmdWebToken([]string{"-token-file", path}); err != nil {
			t.Fatalf("web-token: %v", err)
		}
	})
	if strings.TrimSpace(out) != token {
		t.Errorf("打印的口令 = %q，want %q", strings.TrimSpace(out), token)
	}

	// 已存在且无 -force：拒绝覆盖。
	if err := cmdWebToken([]string{"-token-file", path, "-init"}); err == nil {
		t.Error("已存在的口令文件应拒绝覆盖（需 -force）")
	}

	// -force：轮换成功且口令确实变化。
	captureStdout(t, func() {
		if err := cmdWebToken([]string{"-token-file", path, "-init", "-force"}); err != nil {
			t.Fatalf("web-token -init -force: %v", err)
		}
	})
	rotated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(rotated)) == token {
		t.Error("轮换后口令应发生变化")
	}

	// 缺失文件：打印口令应报错（不静默创建空口令）。
	if err := cmdWebToken([]string{"-token-file", filepath.Join(dir, "nope")}); err == nil {
		t.Error("缺失口令文件应报错")
	}
}
