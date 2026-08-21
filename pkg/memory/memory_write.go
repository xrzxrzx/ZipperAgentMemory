package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Write 原子写入根内相对路径 rel：overwrite=true 时覆盖已存在文件，
// overwrite=false（默认）时目标已存在返回 ErrExists。
//
// 原子写流程（规范 §6.1）：同目录临时文件 → 写入 → fsync → rename 覆盖；
// 中途失败只遗留可清理的 .tmp-* 临时文件，不会出现半写的目标文件。
// 写操作由互斥锁串行化（设计文档 §6.2）。
func (s *Store) Write(rel string, content []byte, overwrite bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.resolve(rel)
	if err != nil {
		return err
	}
	if !overwrite {
		if _, err := os.Lstat(p); err == nil {
			return ErrExists
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("memory: lstat %q: %w", rel, err)
		}
	}
	return atomicWriteFile(p, content)
}

// Append 把 content 追加到根内相对路径 rel 的文件末尾，自动加时间戳分隔行。
//
// 格式：已有内容与新增内容之间插入空行 + "<!-- appended <RFC3339> -->" 注释行；
// 文件不存在时自动创建（含父目录）。追加同样走原子写（读改写 + rename），
// 崩溃不产生半行；由互斥锁串行化。
func (s *Store) Append(rel, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.resolve(rel)
	if err != nil {
		return err
	}
	var existing []byte
	if _, err := os.Lstat(p); err == nil {
		existing, err = os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("memory: read for append %q: %w", rel, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("memory: lstat %q: %w", rel, err)
	}

	header := fmt.Sprintf("<!-- appended %s -->\n", time.Now().Format(time.RFC3339))
	var b strings.Builder
	if len(existing) > 0 {
		b.Write(existing)
		if existing[len(existing)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString("\n") // 分隔空行
	}
	b.WriteString(header)
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	return atomicWriteFile(p, []byte(b.String()))
}

// atomicWriteFile 把 content 原子写入 target：
// 同目录临时文件（.tmp-*）→ 写入 → fsync → chmod 0644 → rename 覆盖。
// 失败时清理临时文件；目录 fsync 为尽力而为（Windows 不支持时忽略）。
func atomicWriteFile(target string, content []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("memory: mkdir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("memory: create temp in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName) // 清理失败的临时文件无副作用，仅遗留可手动删除的 .tmp-* 文件
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close() // 关闭失败不影响错误主因
		return fmt.Errorf("memory: write temp %q: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("memory: fsync temp %q: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("memory: chmod temp %q: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("memory: close temp %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("memory: rename %q -> %q: %w", tmpName, target, err)
	}
	tmpName = "" // 已 rename 成功，临时文件不再需要清理
	syncDir(dir)
	return nil
}

// syncDir 对目录执行 fsync，使 rename 持久化（Linux 上保证崩溃后可恢复）；
// Windows 不支持对目录句柄 Sync，失败时静默忽略（规范 §6.1 的 fsync 指文件本身）。
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
