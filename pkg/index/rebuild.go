package index

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"zipper-agent-memory/pkg/memory"
)

// indexableExts 是可进索引的常规文本文件扩展名（小写，含点前缀）。
// 记忆库内容为 Markdown / CSV / Markdown 表格（design.md §5），
// 另兼容常见纯文本配置格式；其他文件（图片、二进制等）一律不进索引。
var indexableExts = map[string]struct{}{
	".md":       {},
	".markdown": {},
	".csv":      {},
	".tsv":      {},
	".txt":      {},
	".json":     {},
	".yaml":     {},
	".yml":      {},
	".toml":     {},
}

// ShouldIndex 报告 rel（相对 memory/ 根的路径，正斜杠或本机分隔符均可）
// 是否应进入索引：
//   - 目录：可索引（除隐藏目录外），供遍历剪枝判断；
//   - 文件：跳过隐藏文件（含 .git/ 内容与索引库自身）、原子写遗留的 .tmp-*、
//     以及非 indexableExts 扩展名的文件。
//
// 注意 .tmp-* 以 "." 开头，隐藏规则已覆盖，显式保留该分支为自文档化。
// 记忆库根目录本身（rel == "." 或空）不索引（任务书：memory/ 目录本身不进入索引）。
func ShouldIndex(rel string, isDir bool) bool {
	if rel == "" || rel == "." {
		return false
	}
	base := filepath.Base(filepath.FromSlash(rel))
	if strings.HasPrefix(base, ".") {
		return false
	}
	if strings.HasPrefix(base, ".tmp-") {
		return false
	}
	if isDir {
		return true
	}
	_, ok := indexableExts[strings.ToLower(filepath.Ext(base))]
	return ok
}

// Rebuild 全量重建索引：遍历 memory/ 目录树，把现存可索引文件全部写入，
// 然后清空重建（索引与磁盘在返回时刻严格一致）。
//
// 实现：单事务内 DROP 两表并重建 schema，再逐文件读入 Upsert——
// 事务性 DDL 保证中途失败不留半态（derived state，可再次 rebuild）；
// 期间并发读者（如 CLI search）在 WAL 快照上读到旧索引，提交后即见新索引
// （设计文档 §9 R7：不加锁强一致）。
//
// 返回索引到的文件数。
func (ix *Index) Rebuild(store *memory.Store) (int, error) {
	root := store.Root()
	relPaths, err := collectIndexableFiles(root)
	if err != nil {
		return 0, err
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()

	tx, err := ix.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("index: rebuild begin: %w", err)
	}
	defer tx.Rollback()

	// 清空并重建 schema。
	if _, err := tx.Exec(`DROP TABLE IF EXISTS docs_fts`); err != nil {
		return 0, fmt.Errorf("index: rebuild drop fts: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE IF EXISTS docs`); err != nil {
		return 0, fmt.Errorf("index: rebuild drop docs: %w", err)
	}
	if _, err := tx.Exec(schema); err != nil {
		return 0, fmt.Errorf("index: rebuild create schema: %w", err)
	}

	count := 0
	for _, rel := range relPaths {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err != nil {
			return 0, fmt.Errorf("index: rebuild read %s: %w", rel, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return 0, fmt.Errorf("index: rebuild stat %s: %w", rel, err)
		}
		m, body := ParseFrontMatter(data)
		m.MTime = info.ModTime()
		m.Size = info.Size()
		if err := upsertTx(tx, rel, m, []byte(body)); err != nil {
			return 0, err
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("index: rebuild commit: %w", err)
	}
	return count, nil
}

// upsertTx 在既有事务内执行 Upsert 的两表写入（Rebuild 复用，避免套嵌事务）。
func upsertTx(tx *sql.Tx, path string, m Meta, content []byte) error {
	if _, err := tx.Exec(`
INSERT INTO docs(path, mtime, size, tags, created, source)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	mtime = excluded.mtime,
	size = excluded.size,
	tags = excluded.tags,
	created = excluded.created,
	source = excluded.source`,
		path, m.MTime.Unix(), m.Size, m.Tags, m.Created, m.Source); err != nil {
		return fmt.Errorf("index: rebuild upsert %q docs: %w", path, err)
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM docs WHERE path = ?`, path).Scan(&id); err != nil {
		return fmt.Errorf("index: rebuild upsert %q lookup: %w", path, err)
	}
	if _, err := tx.Exec(`DELETE FROM docs_fts WHERE rowid = ?`, id); err != nil {
		return fmt.Errorf("index: rebuild upsert %q fts delete: %w", path, err)
	}
	if _, err := tx.Exec(`
INSERT INTO docs_fts(rowid, tags, created, source, body, path)
VALUES (?, ?, ?, ?, ?, ?)`,
		id, tokenizeForFTS(m.Tags), tokenizeForFTS(m.Created), tokenizeForFTS(m.Source), tokenizeForFTS(string(content)), path); err != nil {
		return fmt.Errorf("index: rebuild upsert %q fts insert: %w", path, err)
	}
	return nil
}

// collectIndexableFiles 遍历 root 目录树，返回全部可索引文件的相对路径
// （正斜杠分隔，排序稳定）。隐藏目录（含 .git）直接剪枝。
func collectIndexableFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("index: walk %q: %w", p, err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("index: rel %q: %w", p, err)
		}
		if d.IsDir() {
			if rel == "." {
				return nil // 根目录本身不索引、也不剪枝（否则整棵树被 SkipDir）
			}
			if !ShouldIndex(rel, true) {
				return filepath.SkipDir // 隐藏目录（.git 等）不进入索引也不下钻
			}
			return nil
		}
		if ShouldIndex(rel, false) {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
