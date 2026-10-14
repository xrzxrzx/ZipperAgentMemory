package memory

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolveRejectsTraversal 覆盖路径穿越被拒：../（两种形态）、绝对路径、
// 带卷名的相对路径。全部表驱动（规范 §8.1）。
func TestResolveRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	absOutside := filepath.Join(t.TempDir(), "secret.txt")
	tests := []struct {
		name string
		rel  string
	}{
		{"parent dotdot", "../x.md"},
		{"nested dotdot", "notes/../../x.md"},
		{"dotdot only", ".."},
		{"dotdot slash", "../"},
		{"absolute path", absOutside},
		{"absolute drive path", `C:\Windows\win.ini`},
		{"volume-relative", `C:foo`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "volume-relative" && filepath.VolumeName(tc.rel) == "" {
				t.Skip("当前平台无卷名语义（非 Windows）")
			}
			if _, err := st.Read(tc.rel); !errors.Is(err, ErrPathOutsideRoot) {
				t.Fatalf("Read(%q) 期望 ErrPathOutsideRoot，实际 %v", tc.rel, err)
			}
			if _, err := st.resolve(tc.rel); !errors.Is(err, ErrPathOutsideRoot) {
				t.Fatalf("resolve(%q) 期望 ErrPathOutsideRoot，实际 %v", tc.rel, err)
			}
		})
	}
}

// TestResolveAllowsInRoot 覆盖合法路径（含不存在的新路径）不被误伤。
func TestResolveAllowsInRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".", "notes", "notes/x.md", "notes/deep/new/file.md"} {
		p, err := st.resolve(rel)
		if err != nil {
			t.Fatalf("resolve(%q): %v", rel, err)
		}
		if !withinRoot(st.resolvedRoot, p) {
			t.Fatalf("resolve(%q) = %q 不在根内", rel, p)
		}
	}
}

// createDirSymlink 创建目录符号链接：优先 os.Symlink（需要管理员/开发者模式），
// Windows 无权限时回退 cmd mklink /J 目录联接（junction，无需管理员）。
// 两者都失败时返回错误，由调用方决定跳过。
func createDirSymlink(t *testing.T, link, target string) error {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// junction 目标必须是绝对目录路径
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, absTarget)
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.New(string(out))
	}
	return nil
}

// TestSymlinkDirEscapeRejected 覆盖符号链接（目录）逃逸：根内链接指向根外目录。
// Windows 本机以 junction 实测（os.Symlink 需管理员权限时回退）。
func TestSymlinkDirEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "f.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "evil")
	if err := createDirSymlink(t, linkDir, outsideDir); err != nil {
		t.Skipf("无法创建目录符号链接（需要管理员/开发者模式），跳过：%v", err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read("evil/f.md"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Read 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if _, err := st.List("evil"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("List 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if err := st.Write("evil/f.md", []byte("x"), false); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Write 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if err := st.Append("evil/f.md", "x"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Append 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	// 元数据查询同样必须在沙箱处失败（需求-004）。
	if _, err := st.IsDir("evil/f.md"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("IsDir 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if _, err := st.ModTime("evil/f.md"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("ModTime 期望 ErrPathOutsideRoot，实际 %v", err)
	}
}

// TestIsDirAndModTimeSandboxed 覆盖沙箱内元数据查询（需求-004）：
// 越界路径必须在沙箱处失败——**根外目标连 Stat 都不发生**；根内三种形态正常返回。
func TestIsDirAndModTimeSandboxed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}

	// 根内：目录 / 文件 / 根目录 / 不存在。
	if isDir, err := st.IsDir("notes"); err != nil || !isDir {
		t.Errorf("IsDir(notes) = %v, %v；want true, nil", isDir, err)
	}
	if isDir, err := st.IsDir("."); err != nil || !isDir {
		t.Errorf("IsDir(.) = %v, %v；want true, nil", isDir, err)
	}
	if isDir, err := st.IsDir("notes/a.md"); err != nil || isDir {
		t.Errorf("IsDir(notes/a.md) = %v, %v；want false, nil", isDir, err)
	}
	if mt, err := st.ModTime("notes/a.md"); err != nil || mt.IsZero() {
		t.Errorf("ModTime(notes/a.md) = %v, %v；want 非零时间, nil", mt, err)
	}
	if _, err := st.IsDir("notes/nope.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("IsDir(不存在) 期望 os.ErrNotExist，实际 %v", err)
	}
	if _, err := st.ModTime("notes/nope.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ModTime(不存在) 期望 os.ErrNotExist，实际 %v", err)
	}

	// 越界：一律 ErrPathOutsideRoot（绝不 Stat 根外目标）。
	traversal := []string{"..", "../x.md", "notes/../../x.md", filepath.Join(t.TempDir(), "outside")}
	if filepath.VolumeName(`C:\Windows\win.ini`) != "" {
		traversal = append(traversal, `C:\Windows\win.ini`) // 平台无卷名语义时跳过
	}
	for _, rel := range traversal {
		if _, err := st.IsDir(rel); !errors.Is(err, ErrPathOutsideRoot) {
			t.Errorf("IsDir(%q) 期望 ErrPathOutsideRoot，实际 %v", rel, err)
		}
		if _, err := st.ModTime(rel); !errors.Is(err, ErrPathOutsideRoot) {
			t.Errorf("ModTime(%q) 期望 ErrPathOutsideRoot，实际 %v", rel, err)
		}
	}
}

// TestSymlinkFileEscapeRejected 覆盖符号链接（文件）逃逸：根内链接指向根外文件。
// Windows 无管理员权限时跳过（Linux CI 上完整执行）。
func TestSymlinkFileEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("无法创建文件符号链接（需要管理员/开发者模式），跳过：%v", err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read("escape.md"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Read 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if err := st.Write("escape.md", []byte("x"), false); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Write 期望 ErrPathOutsideRoot，实际 %v", err)
	}
}

// TestSymlinkWithinRootAllowed 覆盖根内符号链接（指向根内文件）不误伤。
func TestSymlinkWithinRootAllowed(t *testing.T) {
	root := t.TempDir()
	realFile := filepath.Join(root, "real.md")
	if err := os.WriteFile(realFile, []byte("real content"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias.md")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skipf("无法创建符号链接（需要管理员/开发者模式），跳过：%v", err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := st.Read("alias.md")
	if err != nil {
		t.Fatalf("Read(alias.md): %v", err)
	}
	if string(data) != "real content" {
		t.Fatalf("alias 内容 = %q，期望 %q", data, "real content")
	}
}

// createJunction 强制用 cmd mklink /J 创建目录联接（junction，无需管理员），
// 用于覆盖 Windows 上 EvalSymlinks 不解析 junction 的盲区；非 Windows 返回错误。
func createJunction(t *testing.T, link, target string) error {
	t.Helper()
	if runtime.GOOS != "windows" {
		return errors.New("junction 仅 Windows 支持")
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, absTarget)
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.New(string(out))
	}
	return nil
}

// TestJunctionDirEscapeRejected 覆盖 Windows 目录联接逃逸（junction）：
// 组件级 Readlink 解析必须拦截 read/write/append/list 全部四条路径。
func TestJunctionDirEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "f.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "evil")
	if err := createJunction(t, linkDir, outsideDir); err != nil {
		t.Skipf("无法创建 junction，跳过：%v", err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read("evil/f.md"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Read 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if err := st.Write("evil/new.md", []byte("x"), false); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Write 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if err := st.Append("evil/f.md", "x"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Append 期望 ErrPathOutsideRoot，实际 %v", err)
	}
	if _, err := st.List("evil"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("List 期望 ErrPathOutsideRoot，实际 %v", err)
	}
}

// TestJunctionWithinRootAllowed 覆盖根内 junction（指向根内目录）不误伤。
func TestJunctionWithinRootAllowed(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "f.md"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "alias")
	if err := createJunction(t, linkDir, realDir); err != nil {
		t.Skipf("无法创建 junction，跳过：%v", err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := st.Read("alias/f.md")
	if err != nil {
		t.Fatalf("Read(alias/f.md): %v", err)
	}
	if string(data) != "inside" {
		t.Fatalf("内容 = %q，期望 inside", data)
	}
}

// TestSymlinkLoopDetected 覆盖符号链接环：解析必须报错而非死循环（深度上限）。
func TestSymlinkLoopDetected(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Skipf("无法创建符号链接，跳过：%v", err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Skipf("无法创建符号链接，跳过：%v", err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read("a"); err == nil {
		t.Fatal("符号链接环应返回错误而非死循环")
	}
}
