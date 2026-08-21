package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"zipper-agent-memory/pkg/memory"
)

// openTestIndex 在临时目录打开一个索引，测试结束自动关闭。
func openTestIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

// mustUpsert 便捷封装：失败即终止测试。
func mustUpsert(t *testing.T, ix *Index, path, body string) {
	t.Helper()
	if err := ix.Upsert(path, Meta{}, []byte(body)); err != nil {
		t.Fatalf("Upsert(%q): %v", path, err)
	}
}

// searchPaths 返回 Search 命中的路径集合（断言查询不报错）。
func searchPaths(t *testing.T, ix *Index, query string) []string {
	t.Helper()
	rs, err := ix.Search(query, 50)
	if err != nil {
		t.Fatalf("Search(%q): %v", query, err)
	}
	paths := make([]string, 0, len(rs))
	for _, r := range rs {
		paths = append(paths, r.Path)
	}
	return paths
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestOpenSetsWALMode(t *testing.T) {
	ix := openTestIndex(t)
	var mode string
	if err := ix.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestUpsertSearchRemove(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/go.md", "Go 语言学习笔记，FTS5 全文检索测试。李白 凤凰 开发")

	// 关键词命中（英文 + 中文，验证 CJK 单字切分后短语匹配）。
	for _, q := range []string{"Go", "FTS5", "李白", "语言", "凤凰"} {
		paths := searchPaths(t, ix, q)
		if !contains(paths, "notes/go.md") {
			t.Errorf("Search(%q) = %v, want contains notes/go.md", q, paths)
		}
	}
	// snippet 非空且包含关键词（中文连续字形还原逻辑在 untokenizeSnippet 单测覆盖）。
	rs, err := ix.Search("李白", 5)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Snippet == "" {
		t.Errorf("snippet empty")
	}
	if !strings.Contains(rs[0].Snippet, "李白") {
		t.Errorf("snippet = %q, want contains 李白", rs[0].Snippet)
	}

	// 删除后同步移除。
	if err := ix.Remove("notes/go.md"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if paths := searchPaths(t, ix, "Go"); len(paths) != 0 {
		t.Errorf("after Remove, Search(Go) = %v, want empty", paths)
	}
	// 幂等删除不报错。
	if err := ix.Remove("notes/go.md"); err != nil {
		t.Fatalf("Remove again: %v", err)
	}
}

func TestUpsertOverwritesContent(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/a.md", "alpha 关键词")
	if paths := searchPaths(t, ix, "alpha"); !contains(paths, "notes/a.md") {
		t.Fatalf("initial upsert missing")
	}
	mustUpsert(t, ix, "notes/a.md", "beta 关键词") // 覆盖：alpha 应消失
	if paths := searchPaths(t, ix, "alpha"); len(paths) != 0 {
		t.Errorf("after overwrite, alpha still found: %v", paths)
	}
	if paths := searchPaths(t, ix, "beta"); !contains(paths, "notes/a.md") {
		t.Errorf("after overwrite, beta missing: %v", paths)
	}
}

func TestSearchLimitAndEmptyQuery(t *testing.T) {
	ix := openTestIndex(t)
	for i := 0; i < 5; i++ {
		mustUpsert(t, ix, filepath.ToSlash(filepath.Join("notes", "f"+string(rune('a'+i))+".md")), "共同关键词 body")
	}
	rs, err := ix.Search("共同", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Errorf("Search(共同, limit=3) = %d hits, want 3", len(rs))
	}
	rs, err = ix.Search("共同", 0) // limit<=0 → 默认 20
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 5 {
		t.Errorf("Search(共同, limit=0) = %d hits, want 5", len(rs))
	}
	if _, err := ix.Search("   ", 5); err != ErrEmptyQuery {
		t.Errorf("Search(blank) err = %v, want ErrEmptyQuery", err)
	}
}

// TestRebuildConsistency 验证：全量重建后搜索结果与「增量全量 Upsert」一致，
// 且隐藏文件/临时文件/非文本文件不进索引。
func TestRebuildConsistency(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"notes/a.md":       "苹果 apple 内容",
		"notes/b.md":       "香蕉 banana 内容",
		"notes/sub/c.md":   "樱桃 cherry 内容",
		"notes/.hidden.md": "隐藏 hidden 不该进索引",
		"notes/.tmp-x":     "临时 tmp 不该进索引",
		"notes/data.bin":   "二进制 binary 不该进索引",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := memory.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}

	ix := openTestIndex(t)
	// 增量状态：只见过 a.md 与 c.md（模拟 watcher 只处理了部分事件）。
	mustUpsert(t, ix, "notes/a.md", "苹果 apple 内容")
	mustUpsert(t, ix, "notes/sub/c.md", "樱桃 cherry 内容")
	if paths := searchPaths(t, ix, "banana"); len(paths) != 0 {
		t.Fatalf("incremental state should not know banana")
	}

	// 全量重建。
	n, err := ix.Rebuild(store)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if n != 3 {
		t.Errorf("Rebuild indexed %d files, want 3", n)
	}

	// 重建后三个文件都可搜到。
	for q, want := range map[string]string{
		"apple": "notes/a.md", "banana": "notes/b.md", "cherry": "notes/sub/c.md",
	} {
		paths := searchPaths(t, ix, q)
		if !contains(paths, want) {
			t.Errorf("after rebuild, Search(%q) = %v, want %s", q, paths, want)
		}
	}
	// 不应进索引的文件。
	for _, q := range []string{"hidden", "tmp", "binary"} {
		if paths := searchPaths(t, ix, q); len(paths) != 0 {
			t.Errorf("after rebuild, Search(%q) = %v, want empty", q, paths)
		}
	}

	// 与「增量全量 Upsert」的一致性：重建结果 == 手工逐条 Upsert 的结果
	// （按路径集合比较；FTS5 对同质内容的 rank 平局排序不稳定，顺序不参与断言）。
	ix2 := openTestIndex(t)
	for rel, content := range files {
		if !ShouldIndex(rel, false) {
			continue
		}
		mustUpsert(t, ix2, rel, content)
	}
	for _, q := range []string{"apple", "banana", "cherry", "内容"} {
		a, b := searchPaths(t, ix, q), searchPaths(t, ix2, q)
		sort.Strings(a)
		sort.Strings(b)
		if len(a) != len(b) {
			t.Errorf("rebuild vs incremental mismatch for %q: %v vs %v", q, a, b)
			continue
		}
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("rebuild vs incremental mismatch for %q: %v vs %v", q, a, b)
				break
			}
		}
	}
}

// TestFrontMatterIndexed 验证 front-matter 元数据（tags/created/source）
// 进入 FTS 可检索，正文已去掉 front-matter 块。
func TestFrontMatterIndexed(t *testing.T) {
	ix := openTestIndex(t)
	content := "---\ntags: [deploy, 部署]\ncreated: 2026-08-01\nsource: manual\n---\n正文开始：服务上线记录"
	m, body := ParseFrontMatter([]byte(content))
	if m.Tags != "deploy 部署" {
		t.Errorf("Tags = %q, want %q", m.Tags, "deploy 部署")
	}
	if m.Created != "2026-08-01" || m.Source != "manual" {
		t.Errorf("Created/Source = %q/%q", m.Created, m.Source)
	}
	if body != "正文开始：服务上线记录" {
		t.Errorf("body = %q", body)
	}
	if err := ix.Upsert("notes/deploy.md", m, []byte(body)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"deploy", "部署", "manual", "2026-08-01", "上线"} {
		paths := searchPaths(t, ix, q)
		if !contains(paths, "notes/deploy.md") {
			t.Errorf("Search(%q) = %v, want notes/deploy.md", q, paths)
		}
	}
	// front-matter 里的字不应出现在正文命中片段来源中（正文不含 "tags" 等词）。
	rs, err := ix.Search("上线", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) == 0 || strings.Contains(rs[0].Snippet, "tags") {
		t.Errorf("snippet should come from body, got %q", rs[0].Snippet)
	}
}

// TestUpsertMetaPersisted 验证 docs 表的 mtime/size 元数据正确落库。
func TestUpsertMetaPersisted(t *testing.T) {
	ix := openTestIndex(t)
	when := time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC)
	if err := ix.Upsert("notes/meta.md", Meta{MTime: when, Size: 123, Tags: "go"}, []byte("正文")); err != nil {
		t.Fatal(err)
	}
	var mtime int64
	var size int64
	var tags string
	if err := ix.db.QueryRow(`SELECT mtime, size, tags FROM docs WHERE path = ?`, "notes/meta.md").Scan(&mtime, &size, &tags); err != nil {
		t.Fatal(err)
	}
	if mtime != when.Unix() || size != 123 || tags != "go" {
		t.Errorf("docs row = (%d, %d, %q), want (%d, 123, go)", mtime, size, tags, when.Unix())
	}
}

// TestShouldIndexBoundaries 覆盖 ShouldIndex 判定边界（Rebuild 遍历与
// watcher 增量共用）：根目录本身、隐藏文件/目录、.tmp-* 原子写遗留、
// 扩展名大小写、非文本扩展名、正斜杠/本机分隔符混合输入。
func TestShouldIndexBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		rel   string
		isDir bool
		want  bool
	}{
		{"根目录本身不索引", ".", false, false},
		{"空路径不索引", "", false, false},
		{"普通 md 文件", "notes/a.md", false, true},
		{"大写扩展名同样可索引", "notes/A.MD", false, true},
		{"csv 可索引", "structured/tasks.csv", false, true},
		{"tsv 可索引", "structured/t.tsv", false, true},
		{"txt 可索引", "notes/raw.txt", false, true},
		{"json 可索引", "meta/config.json", false, true},
		{"yaml 可索引", "meta/c.yaml", false, true},
		{"toml 可索引", "meta/c.toml", false, true},
		{"二进制不进索引", "notes/photo.png", false, false},
		{"无扩展名不进索引", "notes/README", false, false},
		{"隐藏文件不进索引", "notes/.hidden.md", false, false},
		{"原子写临时文件不进索引", "notes/.tmp-12345", false, false},
		{"隐藏目录返回可索引(供剪枝判断)", ".git", true, false},
		{"普通目录返回可索引(供遍历下钻)", "notes/sub", true, true},
		{"隐藏深层目录剪枝", "notes/.cache", true, false},
		{"Windows 分隔符输入", `notes\sub\a.md`, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldIndex(tt.rel, tt.isDir); got != tt.want {
				t.Errorf("ShouldIndex(%q, isDir=%v) = %v, want %v", tt.rel, tt.isDir, got, tt.want)
			}
		})
	}
}

// TestRebuildEmptyDir 边界：空记忆库重建返回 0 且无错误，搜索空结果。
func TestRebuildEmptyDir(t *testing.T) {
	store, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ix := openTestIndex(t)
	n, err := ix.Rebuild(store)
	if err != nil {
		t.Fatalf("Rebuild(empty): %v", err)
	}
	if n != 0 {
		t.Errorf("Rebuild(empty) = %d files, want 0", n)
	}
	if paths := searchPaths(t, ix, "anything"); len(paths) != 0 {
		t.Errorf("empty rebuild should have no hits, got %v", paths)
	}
}

// TestRebuildPrunesHiddenDir 边界：隐藏目录（.git 等）整体不进索引，
// 其内即使有可索引扩展名文件也不进入（collectIndexableFiles 剪枝）。
func TestRebuildPrunesHiddenDir(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"notes/visible.md":     "可见内容 visible",
		".git/hooks/sample.md": "git 内部 sample 不该进索引",
		".hidden/inner.md":     "隐藏目录 inner 不该进索引",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := memory.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ix := openTestIndex(t)
	n, err := ix.Rebuild(store)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if n != 1 {
		t.Errorf("Rebuild = %d files, want 1（隐藏目录内文件被剪枝）", n)
	}
	for _, q := range []string{"sample", "inner", "visible"} {
		paths := searchPaths(t, ix, q)
		if q == "visible" && !contains(paths, "notes/visible.md") {
			t.Errorf("Search(%q) 应命中 visible.md，实际 %v", q, paths)
		}
		if q != "visible" && len(paths) != 0 {
			t.Errorf("Search(%q) 应无命中（隐藏目录剪枝），实际 %v", q, paths)
		}
	}
}

// TestRebuildIdempotent 边界：重复重建结果一致（幂等），且重建后可再次
// 增量 Upsert/Remove（重建未破坏表的可用性）。
func TestRebuildIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("苹果 apple"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := memory.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ix := openTestIndex(t)
	if _, err := ix.Rebuild(store); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Rebuild(store); err != nil {
		t.Fatalf("second Rebuild: %v", err)
	}
	if paths := searchPaths(t, ix, "苹果"); !contains(paths, "notes/a.md") {
		t.Errorf("重复重建后仍应命中，实际 %v", paths)
	}
	// 重建后增量能力未损坏。
	mustUpsert(t, ix, "notes/b.md", "香蕉 banana")
	if paths := searchPaths(t, ix, "banana"); !contains(paths, "notes/b.md") {
		t.Errorf("重建后 Upsert 未生效：%v", paths)
	}
	if err := ix.Remove("notes/a.md"); err != nil {
		t.Fatalf("重建后 Remove: %v", err)
	}
	if paths := searchPaths(t, ix, "苹果"); len(paths) != 0 {
		t.Errorf("重建后 Remove 未生效：%v", paths)
	}
}
