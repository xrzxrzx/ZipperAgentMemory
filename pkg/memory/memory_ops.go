package memory

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry 是 List 返回的目录项。
type Entry struct {
	Name  string // 目录项名（不含路径）
	IsDir bool   // 是否为子目录
}

// List 列出根内相对路径 dir 下的目录项（按名称排序，不递归）。
// dir 为空或 "." 表示根目录；路径同样经沙箱校验。
func (s *Store) List(dir string) ([]Entry, error) {
	p, err := s.resolve(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("memory: list %q: %w", dir, err)
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, Entry{Name: e.Name(), IsDir: e.IsDir()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// StatusInfo 是 Status 返回的记忆库统计。
type StatusInfo struct {
	FileCount    int       // 普通文件数（不含目录；跳过原子写遗留的 .tmp-* 临时文件）
	TotalBytes   int64     // 全部文件总大小（字节）
	LastModified time.Time // 最近一次文件修改时间
}

// Status 统计记忆库：文件数、总大小、最近变更时间（递归遍历全部目录，只读）。
func (s *Store) Status() (StatusInfo, error) {
	var st StatusInfo
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("memory: walk %q: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return nil // 原子写遗留的临时文件不计入统计
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("memory: stat %q: %w", path, err)
		}
		st.FileCount++
		st.TotalBytes += info.Size()
		if info.ModTime().After(st.LastModified) {
			st.LastModified = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return StatusInfo{}, err
	}
	return st, nil
}
