package index

import (
	"database/sql"
	"errors"
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

// searchPathsMode 返回指定语义下 SearchMode 命中的路径集合。
func searchPathsMode(t *testing.T, ix *Index, query string, mode Mode) []string {
	t.Helper()
	rs, err := ix.SearchMode(query, 50, mode)
	if err != nil {
		t.Fatalf("SearchMode(%q, %v): %v", query, mode, err)
	}
	paths := make([]string, 0, len(rs))
	for _, r := range rs {
		paths = append(paths, r.Path)
	}
	return paths
}

// sameStrings 报告两个已排序切片是否完全一致。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

// TestSearchMultiTerm 验收（2026-10-05 缺陷修复）：多关键词按词元连接——
// AND 要求全部词元命中，OR 命中任一即可；单词查询行为与旧版一致。
func TestSearchMultiTerm(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/both.md", "隧道 与 端口 都出现")
	mustUpsert(t, ix, "notes/tunnel.md", "只有隧道 的记录")
	mustUpsert(t, ix, "notes/port.md", "只有端口 的记录")

	// AND：两词都命中才算（旧实现此处为 0 命中——连续短语语义）。
	if got := searchPathsMode(t, ix, "隧道 端口", ModeAND); !sameStrings(got, []string{"notes/both.md"}) {
		t.Errorf("AND 查询 = %v, want [notes/both.md]", got)
	}
	// 兼容入口 Search 默认即 AND。
	if got := searchPaths(t, ix, "隧道 端口"); !sameStrings(got, []string{"notes/both.md"}) {
		t.Errorf("Search(隧道 端口) = %v, want [notes/both.md]", got)
	}
	// OR：任一命中即可。
	got := searchPathsMode(t, ix, "隧道 端口", ModeOR)
	sort.Strings(got)
	if want := []string{"notes/both.md", "notes/port.md", "notes/tunnel.md"}; !sameStrings(got, want) {
		t.Errorf("OR 查询 = %v, want %v", got, want)
	}
	// 单词查询两种语义一致。
	for _, mode := range []Mode{ModeAND, ModeOR} {
		if got := searchPathsMode(t, ix, "隧道", mode); len(got) != 2 {
			t.Errorf("单词查询 mode=%v = %v, want 2 命中", mode, got)
		}
	}
	// 词元内部仍按连续短语匹配（CJK 单字切分语义未变）。
	mustUpsert(t, ix, "notes/lang.md", "语言学习笔记")
	if got := searchPathsMode(t, ix, "语言", ModeAND); !contains(got, "notes/lang.md") {
		t.Errorf("SearchMode(语言) = %v, want 含 notes/lang.md", got)
	}
}

// TestSearchModeEdgeCases 覆盖无效与危险查询串：纯空白、纯标点、含引号/运算符。
func TestSearchModeEdgeCases(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/a.md", "正常内容 隧道")

	for _, q := range []string{"   ", "\t\n", `"""`, "…", "-"} {
		if _, err := ix.SearchMode(q, 5, ModeAND); err != ErrEmptyQuery {
			t.Errorf("SearchMode(%q) err = %v, want ErrEmptyQuery", q, err)
		}
	}
	// 含双引号 / FTS5 运算符字符的输入不得触发 MATCH 语法错误。
	for _, q := range []string{`"隧道`, `隧道"`, `隧道 AND`, "隧道*", "隧道( )"} {
		if _, err := ix.SearchMode(q, 5, ModeAND); err != nil {
			t.Errorf("SearchMode(%q) 不应报错: %v", q, err)
		}
	}
}

// TestModeString 保证模式文本表示稳定（CLI/MCP/Web 层回显依赖它）。
func TestModeString(t *testing.T) {
	if ModeAND.String() != "and" || ModeOR.String() != "or" {
		t.Errorf("Mode.String() = %q/%q, want and/or", ModeAND.String(), ModeOR.String())
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

// ===== 需求-005 检索质量：先红后绿基线 =====

// TestBigramPrecision（AC6）查询「生日」只应命中连续出现「生日」的文件，
// 不得命中被标点隔开的「生，日」。
//
// 红线基线（T1，改动前）：b.md 会被单字短语 "生 日" 误命中 → 本用例 FAIL，
// 该 FAIL 输出即红档；批次 B（T7/T8）后转绿。
func TestBigramPrecision(t *testing.T) {
	// 红档（批次 A 期间）证据：查「生日」命中 notes/a.md + notes/b.md，
	// 其中 b.md 的「生，日」被单字短语 "生 日" 误命中。原始输出见 验收.md §1。
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/a.md", "我的生日是三月十日")
	mustUpsert(t, ix, "notes/b.md", "生，日这两个字被中文逗号隔开")
	got := searchPaths(t, ix, "生日")
	sort.Strings(got)
	if !sameStrings(got, []string{"notes/a.md"}) {
		t.Fatalf("查「生日」应只命中 notes/a.md，实际命中 %v（其中 notes/b.md 为误命中）", got)
	}
}

// TestPunctuationQuery（P1-1 回归）CJK 关键词附带标点/引号/括号时不得变成 0 命中。
// 批 B 初版把纯标点段当成「必须存在的短语」AND 进表达式，该短语不含 token ⇒
// 表达式恒假 ⇒ 正常输入被静默吞掉（独立验证发现并实测旧版 9 篇全命中、新版全 0）。
func TestPunctuationQuery(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/birthday.md", "我的生日是三月十日")
	for _, q := range []string{
		"生日", "生日,", "生日：", "生日。", "「生日」", "(生日)", `"生日"`, "生日*", "生日!",
	} {
		if got := searchPaths(t, ix, q); !contains(got, "notes/birthday.md") {
			t.Errorf("查 %q 应命中 notes/birthday.md，实际 %v", q, got)
		}
	}
}

// TestSingleCharQuery（AC7）单字中文查询必须命中——这是 AC7 的直接用例。
// 原验收引用 TestSearchChineseHit（查「语言」两字）属证据错引（P1-4）。
func TestSingleCharQuery(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/a.md", "生理与日常记录")
	mustUpsert(t, ix, "notes/b.md", "生，日这两个字被标点隔开")
	for _, path := range []string{"notes/a.md", "notes/b.md"} {
		if got := searchPaths(t, ix, "生"); !contains(got, path) {
			t.Errorf("单字「生」应命中 %s，实际 %v", path, got)
		}
	}
}

// TestOldSchemaEmptyDocsIsStale（P2-1）旧 5 列表结构 + docs 为空，不得被误判为
// 全新库——否则后续写入会永久报「no column named body_bg」，且永不触发 ErrIndexStale。
func TestOldSchemaEmptyDocsIsStale(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.sqlite")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE docs (
	id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE, mtime INTEGER NOT NULL,
	size INTEGER NOT NULL, tags TEXT NOT NULL DEFAULT '',
	created TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT ''
);
CREATE VIRTUAL TABLE docs_fts USING fts5(tags, created, source, body, path UNINDEXED);
`); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	raw.Close()

	ix, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()
	if !ix.stale.Load() {
		t.Fatal("旧 5 列表结构应判为 stale（即使 docs 为空）")
	}
}

// TestRemoveRejectsStale（P2-2）stale 索引上 Remove 必须拒绝，
// 否则 watcher 会继续修改旧格式索引。
func TestRemoveRejectsStale(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stale.sqlite")
	ix, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustUpsert(t, ix, "notes/a.md", "苹果")
	ix.Close()

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
	raw.Close()

	ix2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open stale: %v", err)
	}
	defer ix2.Close()
	if err := ix2.Remove("notes/a.md"); !errors.Is(err, ErrIndexStale) {
		t.Fatalf("stale 下 Remove 应返回 ErrIndexStale，实际 %v", err)
	}
}

// TestSynonymExpansionBaseline（AC1）现状记录：正文含「密码」时，用英文
// password 查询搜不到（别名扩展尚未实现）。
//
// 本用例是「特征化测试」：改动后仍应通过（未配置别名表 ⇒ 行为不变，见 AC2）；
// 配置别名表后的正向能力见 synonyms_test.go。
func TestSynonymExpansionBaseline(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/pwd.md", "记忆库的主密码保存在结构化目录里")
	if got := searchPaths(t, ix, "password"); len(got) != 0 {
		t.Fatalf("未配置别名表时 password 不应命中，实际 %v", got)
	}
	if got := searchPaths(t, ix, "密码"); len(got) == 0 {
		t.Fatalf("对照项：「密码」本身应能命中，实际 0 命中")
	}
}

// TestSynonymExpansionHit（AC1 正向）配置别名表后，英文别名可命中中文正文，
// 组内对称扩展（中文正词同样命中）。
func TestSynonymExpansionHit(t *testing.T) {
	dir := t.TempDir()
	synPath := filepath.Join(dir, "synonyms.tsv")
	if err := os.WriteFile(synPath, []byte("# 别名表\n密码\tpassword\tpwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Open(filepath.Join(dir, "test.sqlite"), WithSynonyms(synPath))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })

	mustUpsert(t, ix, "notes/pwd.md", "记忆库的主密码保存在结构化目录里")
	for _, q := range []string{"password", "pwd", "密码"} {
		if got := searchPaths(t, ix, q); !contains(got, "notes/pwd.md") {
			t.Errorf("查 %q 应命中 notes/pwd.md（别名扩展），实际 %v", q, got)
		}
	}
}

// TestSearchPathHitFirst（AC3）路径命中应排在仅正文命中之前，即使后者正文词频更高。
func TestSearchPathHitFirst(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/general.md", "隧道 隧道 隧道 隧道 隧道 隧道 与端口说明")
	mustUpsert(t, ix, "notes/隧道配置.md", "隧道 与端口")
	rs, err := ix.Search("隧道", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rs) < 2 {
		t.Fatalf("应至少命中 2 篇，实际 %d", len(rs))
	}
	if rs[0].Path != "notes/隧道配置.md" {
		t.Errorf("路径命中的文件应排第一，实际首位 %q", rs[0].Path)
	}
}

// TestSearchScore（AC4）结果带非零分数，且非路径优先组内按分数降序。
func TestSearchScore(t *testing.T) {
	ix := openTestIndex(t)
	mustUpsert(t, ix, "notes/a.md", "苹果 苹果 苹果 苹果")
	mustUpsert(t, ix, "notes/b.md", "苹果 香蕉 樱桃 苹果 苹果")
	rs, err := ix.Search("苹果", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rs) < 2 {
		t.Fatalf("应至少命中 2 篇，实际 %d", len(rs))
	}
	for i, r := range rs {
		if r.Score == 0 {
			t.Errorf("第 %d 条分数为 0（应非零）", i)
		}
		if i > 0 && rs[i-1].Score < r.Score {
			t.Errorf("分数应降序：前 %f < 后 %f", rs[i-1].Score, r.Score)
		}
	}
}

// TestSnippetLength（AC5）命中片段由 24 token 放宽到 48 token。
func TestSnippetLength(t *testing.T) {
	ix := openTestIndex(t)
	body := strings.Repeat("前文无关铺垫内容", 20) + "关键词" + strings.Repeat("后文无关铺垫内容", 20)
	mustUpsert(t, ix, "notes/long.md", body)
	rs, err := ix.Search("关键词", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("应命中 1 篇，实际 %d", len(rs))
	}
	if n := len([]rune(rs[0].Snippet)); n < 40 {
		t.Errorf("片段应达约 48 token 规模（字符数 >= 40），实际 %d 字符：%q", n, rs[0].Snippet)
	}
}

// TestOpenMarksFreshIndex（AC8）新建空库应被标为当前版本，否则首次使用就要 rebuild。
func TestOpenMarksFreshIndex(t *testing.T) {
	ix := openTestIndex(t)
	if ix.stale.Load() {
		t.Fatal("新建空库不应被标记为 stale")
	}
	if _, err := ix.Search("任意", 10); err != nil {
		t.Fatalf("空库检索不应报错：%v", err)
	}
}

// TestIndexStaleDetection（AC8）旧版索引（user_version=0 且已有数据）必须被识别为
// 过时：Search / Upsert 一律返回 ErrIndexStale，不得静默使用旧索引；rebuild 后恢复。
func TestIndexStaleDetection(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "stale.sqlite")

	ix, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustUpsert(t, ix, "notes/a.md", "苹果 banana")
	ix.Close()

	// 模拟旧版索引：把内容格式版本退回 0（此时 docs 表已有数据）。
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen raw: %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
	raw.Close()

	ix2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open stale: %v", err)
	}
	defer ix2.Close()
	if !ix2.stale.Load() {
		t.Fatal("旧版索引应被标记为 stale")
	}
	if _, err := ix2.Search("苹果", 10); !errors.Is(err, ErrIndexStale) {
		t.Fatalf("旧索引 Search 应返回 ErrIndexStale，实际 %v", err)
	}
	if err := ix2.Upsert("notes/b.md", Meta{}, []byte("樱桃")); !errors.Is(err, ErrIndexStale) {
		t.Fatalf("旧索引 Upsert 应返回 ErrIndexStale，实际 %v", err)
	}

	// 准备一个真实文件供 rebuild 索引，重建后应恢复可用且版本写回。
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "a.md"), []byte("苹果 banana"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := memory.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := ix2.Rebuild(store); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if ix2.stale.Load() {
		t.Fatal("rebuild 后不应仍为 stale")
	}
	if got := searchPaths(t, ix2, "苹果"); !contains(got, "notes/a.md") {
		t.Errorf("rebuild 后应可检索，实际 %v", got)
	}
}
