package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestList 覆盖根目录与子目录列表：条目完整、按名称排序、目录/文件标记正确。
func TestList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("# index"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.List(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("根目录应 2 项，实际 %d: %+v", len(entries), entries)
	}
	if entries[0].Name != "index.md" || entries[0].IsDir {
		t.Fatalf("entries[0] = %+v，期望 index.md 文件", entries[0])
	}
	if entries[1].Name != "notes" || !entries[1].IsDir {
		t.Fatalf("entries[1] = %+v，期望 notes 目录", entries[1])
	}
	notes, err := st.List("notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Name != "a.md" {
		t.Fatalf("notes = %+v，期望仅 a.md", notes)
	}
}

// TestListRejectsOutside 覆盖 List 的路径穿越也被拒。
func TestListRejectsOutside(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.List(".."); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("List(..) 期望 ErrPathOutsideRoot，实际 %v", err)
	}
}

// TestStatus 覆盖 Status 统计：文件数（跳过目录与 .tmp-* 临时文件）、
// 总大小、最近修改时间非零且合理。
func TestStatus(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("aaaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "b.md"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tmp-123"), []byte("xxxx"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Status()
	if err != nil {
		t.Fatal(err)
	}
	if info.FileCount != 2 {
		t.Fatalf("文件数 = %d，期望 2（跳过 .tmp-*）", info.FileCount)
	}
	if info.TotalBytes != 8 {
		t.Fatalf("总大小 = %d，期望 8", info.TotalBytes)
	}
	if info.LastModified.IsZero() {
		t.Fatal("LastModified 不应为零值")
	}
	oldest := time.Now().Add(-time.Hour)
	if info.LastModified.Before(oldest) {
		t.Fatalf("LastModified 异常早：%v", info.LastModified)
	}
}
