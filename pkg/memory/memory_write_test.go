package memory

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestWriteCreatesFile 覆盖写入新文件与父目录自动创建。
func TestWriteCreatesFile(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Write("notes/a.md", []byte("hello"), false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("内容 = %q，期望 %q", data, "hello")
	}
	info, err := os.Stat(filepath.Join(root, "notes"))
	if err != nil || !info.IsDir() {
		t.Fatalf("notes 父目录应自动创建: %v", err)
	}
}

// TestWriteOverwriteSemantics 覆盖 overwrite 语义：
// 已存在 + overwrite=false → ErrExists 且原内容不被破坏；overwrite=true → 覆盖。
func TestWriteOverwriteSemantics(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Write("f.md", []byte("v1"), false); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("f.md", []byte("v2"), false); !errors.Is(err, ErrExists) {
		t.Fatalf("已存在 + overwrite=false 期望 ErrExists，实际 %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "f.md"))
	if string(data) != "v1" {
		t.Fatalf("拒绝覆盖后原内容应保留 v1，实际 %q", data)
	}
	if err := st.Write("f.md", []byte("v2"), true); err != nil {
		t.Fatalf("overwrite=true 应覆盖成功: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "f.md"))
	if string(data) != "v2" {
		t.Fatalf("覆盖后内容应为 v2，实际 %q", data)
	}
}

// TestWriteAtomicLeavesNoTempFiles 覆盖原子写：多次覆盖后无 .tmp-* 临时文件遗留。
func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := st.Write("f.md", []byte("data"), true); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("原子写遗留临时文件: %s", e.Name())
		}
	}
}

// TestAppendAddsTimestampSeparator 覆盖 append 时间戳分隔行：
// 两次追加各带一个 RFC3339 时间戳注释行，内容顺序正确，文件以换行结尾。
func TestAppendAddsTimestampSeparator(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append("agent/dev/2026-08.md", "first line"); err != nil {
		t.Fatal(err)
	}
	if err := st.Append("agent/dev/2026-08.md", "second line"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "agent", "dev", "2026-08.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	re := regexp.MustCompile(`<!-- appended \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}([+-]\d{2}:\d{2})? -->`)
	matches := re.FindAllString(s, -1)
	if len(matches) != 2 {
		t.Fatalf("期望 2 个时间戳分隔行，实际 %d：%q", len(matches), s)
	}
	i1 := strings.Index(s, "first line")
	i2 := strings.Index(s, "second line")
	if i1 == -1 || i2 == -1 || i1 > i2 {
		t.Fatalf("追加顺序错误：%q", s)
	}
	if strings.Index(s, matches[1]) > i2 {
		t.Fatalf("第二个时间戳应位于 second line 之前：%q", s)
	}
	if !strings.HasSuffix(s, "\n") {
		t.Fatalf("文件应以换行结尾：%q", s)
	}
}

// TestAppendCreatesMissingFile 覆盖 append 目标文件不存在时自动创建。
func TestAppendCreatesMissingFile(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append("notes/new.md", "hello"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes", "new.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("内容缺失：%q", data)
	}
}

// TestAppendExistingNoTrailingNewline 覆盖原文件末尾无换行时正确衔接分隔行。
func TestAppendExistingNoTrailingNewline(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "f.md"), []byte("no-newline"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Append("f.md", "more"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "f.md"))
	if !strings.Contains(string(data), "no-newline\n\n<!-- appended") {
		t.Fatalf("缺少换行衔接：%q", data)
	}
}
