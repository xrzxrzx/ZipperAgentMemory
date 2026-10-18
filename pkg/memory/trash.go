package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// trashDir 是回收站目录名。**点前缀**是刻意选择：索引的 ShouldIndex 与阅读页
// 点目录策略都会自动跳过它，从而天然满足"回收站不污染检索与目录树"（需求-006 D4）。
const trashDir = ".trash"

// trashTimeLayout 是批次目录名的时间格式，同时作为删除时间的来源。
const trashTimeLayout = "20060102-150405"

var (
	// ErrTrashTargetExists 表示恢复目标位置已被占用——不覆盖（需求-006 AC4）。
	ErrTrashTargetExists = errors.New("memory: restore target already exists")
	// ErrTrashEntryNotFound 表示回收站条目不存在。
	ErrTrashEntryNotFound = errors.New("memory: trash entry not found")
)

// TrashEntry 是回收站中的一条记录。
type TrashEntry struct {
	ID        string    // "<批次>/<原相对路径>"（正斜杠分隔），恢复/清除的入参
	Path      string    // 原相对路径（正斜杠）
	DeletedAt time.Time // 删除时间（取自批次目录名）
	Size      int64     // 字节数
}

// MoveToTrash 把 rel 指向的文件移入回收站，返回条目 ID。
//
// 目录结构：`.trash/<YYYYMMDD-HHMMSS>/<原相对路径>`——一次删除一个批次，
// 同名文件跨批次不冲突，恢复只需取批次下的相对路径。
func (s *Store) MoveToTrash(rel string) (string, error) {
	src, err := s.resolve(rel) // 沙箱校验：越界一律 ErrPathOutsideRoot
	if err != nil {
		return "", err
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("memory: trash %q: %w", rel, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("memory: trash %q: is a directory", rel)
	}

	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: %q", ErrPathOutsideRoot, rel)
	}

	batch := time.Now().Format(trashTimeLayout)
	dstDir := filepath.Join(s.root, trashDir, batch, filepath.FromSlash(clean))
	// 同一批次内同路径重复删除：加后缀，绝不覆盖已有条目。
	dst := dstDir
	for i := 1; ; i++ {
		if _, statErr := os.Stat(dst); os.IsNotExist(statErr) {
			break
		}
		dst = dstDir + fmt.Sprintf(".%d", i)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("memory: trash mkdir: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("memory: trash move %q: %w", rel, err)
	}

	trashRoot := filepath.Join(s.root, trashDir)
	idRel, err := filepath.Rel(trashRoot, dst)
	if err != nil {
		return "", fmt.Errorf("memory: trash rel: %w", err)
	}
	return filepath.ToSlash(idRel), nil
}

// ListTrash 返回回收站全部条目，按删除时间倒序。回收站不存在时返回空列表。
func (s *Store) ListTrash() ([]TrashEntry, error) {
	trashRoot := filepath.Join(s.root, trashDir)
	batches, err := os.ReadDir(trashRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: list trash: %w", err)
	}

	out := make([]TrashEntry, 0, 8)
	for _, batch := range batches {
		if !batch.IsDir() {
			continue
		}
		deletedAt, perr := time.ParseInLocation(trashTimeLayout, batch.Name(), time.Local)
		if perr != nil {
			continue // 非批次目录（人工放进去的杂物）不计入
		}
		base := filepath.Join(trashRoot, batch.Name())
		walkErr := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			out = append(out, TrashEntry{
				ID:        filepath.ToSlash(filepath.Join(batch.Name(), rel)),
				Path:      filepath.ToSlash(rel),
				DeletedAt: deletedAt,
				Size:      fi.Size(),
			})
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("memory: walk trash %q: %w", batch.Name(), walkErr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return out, nil
}

// RestoreFromTrash 把回收站条目移回原路径。原位置已存在时返回
// ErrTrashTargetExists 且**不覆盖**。
func (s *Store) RestoreFromTrash(id string) error {
	src, target, err := s.resolveTrashID(id)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(target); statErr == nil {
		return fmt.Errorf("%w: %s", ErrTrashTargetExists, id)
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("memory: restore stat target: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("memory: restore mkdir: %w", err)
	}
	if err := os.Rename(src, target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrTrashEntryNotFound, id)
		}
		return fmt.Errorf("memory: restore %q: %w", id, err)
	}
	pruneEmptyDir(filepath.Dir(src))
	return nil
}

// PurgeTrash 物理删除一条回收站条目（不可逆）。
func (s *Store) PurgeTrash(id string) error {
	src, _, err := s.resolveTrashID(id)
	if err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrTrashEntryNotFound, id)
		}
		return fmt.Errorf("memory: purge %q: %w", id, err)
	}
	pruneEmptyDir(filepath.Dir(src))
	return nil
}

// ClearTrash 清空整个回收站，返回清除的条目数。
func (s *Store) ClearTrash() (int, error) {
	entries, err := s.ListTrash()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if err := s.PurgeTrash(e.ID); err != nil {
			if errors.Is(err, ErrTrashEntryNotFound) {
				continue
			}
			return n, err
		}
		n++
	}
	_ = os.Remove(filepath.Join(s.root, trashDir)) // 只剩空目录时一并清掉
	return n, nil
}

// TrashOriginalPath 从条目 ID 中取出原相对路径（正斜杠）。**不做文件系统访问**，
// 供调用方在恢复后重新索引时定位文件。
func TrashOriginalPath(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("memory: empty trash id")
	}
	slash := filepath.ToSlash(strings.TrimSpace(id))
	// 绝对路径与卷名一律拒绝：Windows 下 `\etc\passwd` 不算 IsAbs，
	// 若只看 Clean 结果会被当成相对路径放过（回归用例已覆盖）。
	if strings.HasPrefix(slash, "/") || strings.Contains(slash, ":") {
		return "", fmt.Errorf("%w: invalid trash id %q", ErrPathOutsideRoot, id)
	}
	clean := filepath.Clean(filepath.FromSlash(id))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: invalid trash id %q", ErrPathOutsideRoot, id)
	}
	_, orig, ok := strings.Cut(filepath.ToSlash(clean), "/")
	if !ok || orig == "" {
		return "", fmt.Errorf("%w: invalid trash id %q", ErrPathOutsideRoot, id)
	}
	return orig, nil
}

// resolveTrashID 把条目 ID 解析为「回收站内绝对路径」与「原位置绝对路径」。
//
// 两处都要过沙箱：ID 不得逃出回收站目录，原路径不得逃出记忆库根
// （防止有人构造 `../../etc/passwd` 形态的 ID 把文件恢复到根外）。
func (s *Store) resolveTrashID(id string) (src, target string, err error) {
	if strings.TrimSpace(id) == "" {
		return "", "", fmt.Errorf("memory: empty trash id")
	}
	slashRaw := filepath.ToSlash(strings.TrimSpace(id))
	if strings.HasPrefix(slashRaw, "/") || strings.Contains(slashRaw, ":") {
		return "", "", fmt.Errorf("%w: invalid trash id %q", ErrPathOutsideRoot, id)
	}
	clean := filepath.Clean(filepath.FromSlash(id))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: invalid trash id %q", ErrPathOutsideRoot, id)
	}
	slash := filepath.ToSlash(clean)
	batch, orig, ok := strings.Cut(slash, "/")
	if !ok || batch == "" || orig == "" || strings.Contains(batch, "..") {
		return "", "", fmt.Errorf("%w: invalid trash id %q", ErrPathOutsideRoot, id)
	}

	trashRoot := filepath.Join(s.root, trashDir)
	src = filepath.Join(trashRoot, filepath.FromSlash(clean))
	if !withinRoot(trashRoot, src) {
		return "", "", fmt.Errorf("%w: trash id escapes trash root: %q", ErrPathOutsideRoot, id)
	}
	target = filepath.Join(s.root, filepath.FromSlash(orig))
	if !withinRoot(s.root, target) {
		return "", "", fmt.Errorf("%w: trash id escapes root: %q", ErrPathOutsideRoot, id)
	}
	return src, target, nil
}

// pruneEmptyDir 删除空目录（回收站批次目录在清空后不再需要）；非空时静默跳过。
func pruneEmptyDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	_ = os.Remove(dir)
}
