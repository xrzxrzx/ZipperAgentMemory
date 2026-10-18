package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func trashFixture(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("hello 世界"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

// TestMoveToTrashAndList 软删除：原位置消失、回收站可见、元数据正确（AC1/AC2）。
func TestMoveToTrashAndList(t *testing.T) {
	s, root := trashFixture(t)
	id, err := s.MoveToTrash("notes/a.md")
	if err != nil {
		t.Fatalf("MoveToTrash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "a.md")); !os.IsNotExist(err) {
		t.Fatal("原文件应已移走")
	}
	entries, err := s.ListTrash()
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应有 1 条，实际 %d", len(entries))
	}
	e := entries[0]
	if e.ID != id || e.Path != "notes/a.md" {
		t.Errorf("条目不符：%+v", e)
	}
	if e.Size != int64(len("hello 世界")) {
		t.Errorf("size 不符：%d", e.Size)
	}
	if e.DeletedAt.IsZero() {
		t.Error("删除时间不应为零值")
	}
}

// TestRestoreFromTrash 恢复：内容回到原路径，回收站清空（AC3）。
func TestRestoreFromTrash(t *testing.T) {
	s, root := trashFixture(t)
	id, err := s.MoveToTrash("notes/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreFromTrash(id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes", "a.md"))
	if err != nil {
		t.Fatalf("恢复后应可读: %v", err)
	}
	if string(data) != "hello 世界" {
		t.Errorf("内容不符：%q", data)
	}
	if entries, _ := s.ListTrash(); len(entries) != 0 {
		t.Errorf("回收站应已空，实际 %d", len(entries))
	}
}

// TestRestoreRefusesOverwrite 恢复冲突：目标已存在时报错且不覆盖（AC4）。
func TestRestoreRefusesOverwrite(t *testing.T) {
	s, root := trashFixture(t)
	id, err := s.MoveToTrash("notes/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("新内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = s.RestoreFromTrash(id)
	if !errors.Is(err, ErrTrashTargetExists) {
		t.Fatalf("应返回 ErrTrashTargetExists，实际 %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "notes", "a.md"))
	if string(data) != "新内容" {
		t.Errorf("已存在文件不应被覆盖，实际 %q", data)
	}
}

// TestPurgeAndClearTrash 清除与清空（AC5）。
func TestPurgeAndClearTrash(t *testing.T) {
	s, root := trashFixture(t)
	id, err := s.MoveToTrash("notes/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeTrash(id); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if entries, _ := s.ListTrash(); len(entries) != 0 {
		t.Fatal("清除后回收站应为空")
	}
	if err := s.PurgeTrash(id); !errors.Is(err, ErrTrashEntryNotFound) {
		t.Errorf("重复清除应返回 ErrTrashEntryNotFound，实际 %v", err)
	}

	for _, n := range []string{"b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(root, "notes", n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.MoveToTrash("notes/" + n); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ClearTrash()
	if err != nil {
		t.Fatalf("ClearTrash: %v", err)
	}
	if n != 2 {
		t.Errorf("应清除 2 条，实际 %d", n)
	}
}

// TestTrashIDRejectsEscape 条目 ID 不得逃出回收站/根（AC9）。
func TestTrashIDRejectsEscape(t *testing.T) {
	s, _ := trashFixture(t)
	bad := []string{"../notes/a.md", "20260101-000000/../../etc/passwd", "/etc/passwd", ""}
	for _, id := range bad {
		if err := s.RestoreFromTrash(id); err == nil {
			t.Errorf("RestoreFromTrash(%q) 应报错", id)
		}
		if _, err := TrashOriginalPath(id); err == nil {
			t.Errorf("TrashOriginalPath(%q) 应报错", id)
		}
	}
}

// TestMoveToTrashRejectsOutsideRoot 软删除同样受沙箱约束。
func TestMoveToTrashRejectsOutsideRoot(t *testing.T) {
	s, _ := trashFixture(t)
	if _, err := s.MoveToTrash("../outside.md"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("越界应返回 ErrPathOutsideRoot，实际 %v", err)
	}
}

// TestMoveToTrashRejectsDir 目录不能软删除（本轮只做单文件）。
func TestMoveToTrashRejectsDir(t *testing.T) {
	s, _ := trashFixture(t)
	if _, err := s.MoveToTrash("notes"); err == nil {
		t.Fatal("目录不应被软删除")
	}
}
