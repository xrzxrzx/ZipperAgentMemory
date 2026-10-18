package index

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

// 哨兵错误（编码规范 §3.3）：供 errors.Is 判断。
var (
	// ErrEmptyQuery 表示搜索查询为空（空白字符串）。
	ErrEmptyQuery = errors.New("index: empty search query")
)

// Meta 是一条文档记录的索引元数据：文件系统属性 + front-matter 提取字段。
// Tags/Created/Source 由 ParseFrontMatter 提取（不存在时为空串）；
// MTime/Size 由调用方从文件 stat 填充。
type Meta struct {
	MTime   time.Time // 文件修改时间（mtime）
	Size    int64     // 文件大小（字节）
	Tags    string    // front-matter tags（空格分隔的规范化形式）
	Created string    // front-matter created
	Source  string    // front-matter source
}

// Result 是 Search 的单条命中。
type Result struct {
	Path    string  // 相对 memory/ 根的路径（正斜杠分隔）
	Snippet string  // 正文命中片段（关键词以 [ ] 高亮，中文已还原为连续字形）
	Score   float64 // 相关性分数：越大越相关；仅在**同一次查询内**可比较
}

// Index 是记忆库的 SQLite FTS5 索引。
//
// 并发模型：写操作（Upsert/Remove/Rebuild）经 ix.mu 串行化；
// 读操作（Search/Close）依赖数据库连接池固定单连接
// （db.SetMaxOpenConns(1)）保证 SQLite 层天然串行，无 SQLITE_BUSY 竞争，
// 不额外取 ix.mu（与写操作共享同一连接池，SQLite 内部排队）。
// 索引是 derived state，方法不假设调用方持有任何文件锁。
type Index struct {
	db  *sql.DB
	mu  sync.Mutex
	syn *synonymCache // 别名表（可为 nil：未启用或不适用）
}

// Option 是 Open 的可选装配项（变参形式，保持既有 Open(dbPath) 调用不变）。
type Option func(*Index)

// WithSynonyms 启用别名扩展，别名表取自 path（通常是 memory/meta/synonyms.tsv）。
// 文件不存在时静默降级为「无别名表」——检索行为与未启用时一致（需求-005 AC2）。
func WithSynonyms(path string) Option {
	return func(ix *Index) { ix.syn = &synonymCache{path: path} }
}

// schema 创建 docs 与 docs_fts 两表（幂等）。
//
// docs_fts 各列：
//   - tags/created/source/body：FTS5 索引列，入库前经 tokenizeForFTS 切分
//     （CJK 单字化，见 doc.go）；snippet 取 body 列（列序 3）；
//   - path：UNINDEXED 列——随行存储但不参与分词与匹配，供结果输出；
//     路径查表走 docs.path 唯一索引，不经 FTS。
const schema = `
CREATE TABLE IF NOT EXISTS docs (
	id      INTEGER PRIMARY KEY,
	path    TEXT NOT NULL UNIQUE,
	mtime   INTEGER NOT NULL,
	size    INTEGER NOT NULL,
	tags    TEXT NOT NULL DEFAULT '',
	created TEXT NOT NULL DEFAULT '',
	source  TEXT NOT NULL DEFAULT ''
);
CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
	tags, created, source, body,
	path UNINDEXED
);
`

// Open 打开（或创建）dbPath 处的索引并返回 Index。
// 打开时确保 WAL 模式与 schema 就绪；dbPath 所在目录必须可写。
//
// opts 为可选装配项（见 WithSynonyms）；不传时行为与旧签名完全一致。
func Open(dbPath string, opts ...Option) (*Index, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("index: open %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	ix := &Index{db: db}
	for _, opt := range opts {
		opt(ix)
	}
	if err := ix.init(); err != nil {
		db.Close()
		return nil, err
	}
	return ix, nil
}

// init 应用连接级 PRAGMA 并创建 schema。
func (ix *Index) init() error {
	// WAL：并发读（CLI search / 阶段 3 MCP 读）不阻塞常驻写方；
	// synchronous=NORMAL 是 WAL 下的推荐档位（崩溃最多丢最近提交，可 rebuild 恢复）；
	// busy_timeout 防御外部进程短暂占用写锁（防御性，进程内已串行化）。
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=3000",
	} {
		if _, err := ix.db.Exec(pragma); err != nil {
			return fmt.Errorf("index: pragma %q: %w", pragma, err)
		}
	}
	if _, err := ix.db.Exec(schema); err != nil {
		return fmt.Errorf("index: create schema: %w", err)
	}
	return nil
}

// Close 关闭索引数据库（WAL 检查点随连接关闭落盘）。
func (ix *Index) Close() error {
	if ix == nil || ix.db == nil {
		return nil
	}
	if err := ix.db.Close(); err != nil {
		return fmt.Errorf("index: close: %w", err)
	}
	return nil
}

// Upsert 写入（或覆盖）path 的索引记录，content 为文件正文（已去 front-matter）。
// path 是相对 memory/ 根的正斜杠路径。幂等：重复调用同一 path 以最新内容为准。
func (ix *Index) Upsert(path string, m Meta, content []byte) error {
	if path == "" {
		return fmt.Errorf("index: upsert empty path")
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()

	tx, err := ix.db.Begin()
	if err != nil {
		return fmt.Errorf("index: upsert %q begin: %w", path, err)
	}
	defer tx.Rollback() // 提交成功后回滚是 no-op

	// 1) 写 docs 行（UPSERT）：冲突即覆盖元数据，rowid 保持不变。
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
		return fmt.Errorf("index: upsert %q docs: %w", path, err)
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM docs WHERE path = ?`, path).Scan(&id); err != nil {
		return fmt.Errorf("index: upsert %q lookup: %w", path, err)
	}

	// 2) 重写 FTS 行：先删旧（rowid 定位），再插新（同 rowid 保持 docs/FTS 关联）。
	if _, err := tx.Exec(`DELETE FROM docs_fts WHERE rowid = ?`, id); err != nil {
		return fmt.Errorf("index: upsert %q fts delete: %w", path, err)
	}
	ftsBody := tokenizeForFTS(string(content))
	if _, err := tx.Exec(`
INSERT INTO docs_fts(rowid, tags, created, source, body, path)
VALUES (?, ?, ?, ?, ?, ?)`,
		id, tokenizeForFTS(m.Tags), tokenizeForFTS(m.Created), tokenizeForFTS(m.Source), ftsBody, path); err != nil {
		return fmt.Errorf("index: upsert %q fts insert: %w", path, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: upsert %q commit: %w", path, err)
	}
	return nil
}

// Remove 删除 path 的索引记录。path 不存在时返回 nil（幂等）。
func (ix *Index) Remove(path string) error {
	if path == "" {
		return nil
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()

	tx, err := ix.db.Begin()
	if err != nil {
		return fmt.Errorf("index: remove %q begin: %w", path, err)
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(`SELECT id FROM docs WHERE path = ?`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return nil // 索引里本就没有该路径，删除幂等
	}
	if err != nil {
		return fmt.Errorf("index: remove %q lookup: %w", path, err)
	}
	if _, err := tx.Exec(`DELETE FROM docs_fts WHERE rowid = ?`, id); err != nil {
		return fmt.Errorf("index: remove %q fts delete: %w", path, err)
	}
	if _, err := tx.Exec(`DELETE FROM docs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("index: remove %q docs delete: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: remove %q commit: %w", path, err)
	}
	return nil
}

// Mode 指定多关键词查询中各词元之间的连接语义（query 按空白切分）。
type Mode int

const (
	// ModeAND 要求全部词元都命中（默认）。
	ModeAND Mode = iota
	// ModeOR 命中任一词元即可。
	ModeOR
)

// String 返回模式的稳定文本表示（"and"/"or"），供 CLI/MCP/Web 层复述与回显。
func (m Mode) String() string {
	if m == ModeOR {
		return "or"
	}
	return "and"
}

// ParseMode 解析模式文本（大小写不敏感）：空串与 "and" 等价（默认全部命中），
// "or" 为任一命中，其余取值返回错误——不静默回退，避免调用方以为语义已生效。
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "and":
		return ModeAND, nil
	case "or":
		return ModeOR, nil
	default:
		return ModeAND, fmt.Errorf("invalid mode %q (want \"and\" or \"or\")", s)
	}
}

// Search 全文检索 query，返回最多 limit 条命中（默认 20），多词元语义为 AND。
// 等价于 SearchMode(query, limit, ModeAND)，保留为兼容入口。
func (ix *Index) Search(query string, limit int) ([]Result, error) {
	return ix.SearchMode(query, limit, ModeAND)
}

// SearchMode 全文检索 query，mode 决定多个词元之间的连接语义。
//
// 查询处理：query 先按空白切分为若干词元，每个词元与入库侧同样经
// tokenizeForFTS 单字切分，再包成 FTS5 双引号短语（内部双引号加倍转义），
// 最后按 mode 以 AND / OR 连接——保证任意用户输入都不触发 MATCH 语法错误，
// 且中文仍按单字短语匹配（"语言" 能命中正文中的「语言学习」）。
//
// 与旧行为（整串当一个连续短语）的差异：`隧道 端口` 旧语义要求四字连续，
// 现语义为「两词都出现」，即用户直觉上的多关键词检索。
//
// 不含任何可检索字符的词元（纯标点等）会被丢弃；全部词元都被丢弃时
// 返回 ErrEmptyQuery（而不是让 FTS5 报语法错误）。
// 命中片段取自正文列（snippet），中文空格已还原。
func (ix *Index) SearchMode(query string, limit int, mode Mode) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = 20
	}
	match := ix.buildMatch(query, mode)
	if match == "" {
		return nil, ErrEmptyQuery
	}

	const (
		// pathBoostFactor / pathFetchCap 控制超额取行：路径命中的行可能排在
		// bm25 靠后，只取 limit 行会漏掉（AC3），故多取后在 Go 侧排序截断。
		pathBoostFactor = 5
		pathFetchCap    = 200
	)
	fetchLimit := limit * pathBoostFactor
	if fetchLimit > pathFetchCap {
		fetchLimit = pathFetchCap
	}

	// snippet 的第 6 个参数是片段保留 token 数（AC5：24 → 48）；
	// bm25 权重按 docs_fts 列序给出（tags, created, source, body, path），
	// path 是 UNINDEXED 列，权重仅占位——路径相关性由 Go 侧加权处理（AC3）。
	rows, err := ix.db.Query(`
SELECT path,
       snippet(docs_fts, 3, '[', ']', '…', 48) AS snippet,
       bm25(docs_fts, 10.0, 1.0, 1.0, 4.0, 0.0) AS rank
FROM docs_fts
WHERE docs_fts MATCH ?
ORDER BY rank
LIMIT ?`, match, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("index: search %q: %w", query, err)
	}
	defer rows.Close()

	terms := strings.Fields(query)
	type scored struct {
		Result
		pathHit bool
	}
	var hits []scored
	for rows.Next() {
		var (
			s    scored
			rank float64
		)
		if err := rows.Scan(&s.Path, &s.Snippet, &rank); err != nil {
			return nil, fmt.Errorf("index: search scan: %w", err)
		}
		s.Snippet = untokenizeSnippet(s.Snippet)
		s.Score = -rank // bm25 返回负值且越小越相关，取反后「越大越相关」
		s.pathHit = pathMatches(s.Path, terms)
		hits = append(hits, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: search rows: %w", err)
	}

	// 路径命中优先（AC3），同组内按相关性降序；稳定排序保证同分顺序确定。
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].pathHit != hits[j].pathHit {
			return hits[i].pathHit
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Result)
	}
	return out, nil
}

// pathMatches 报告 path 是否包含任一词元（大小写不敏感）。路径即"标题"，
// 命中视为强相关信号，用于 AC3 的优先排序。
func pathMatches(path string, terms []string) bool {
	lower := strings.ToLower(path)
	for _, t := range terms {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" && strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// buildMatch 把用户查询构造成 FTS5 MATCH 表达式：按空白切分词元，逐词元
// 单字切分并包成双引号短语，再按 mode 用 AND / OR 连接；无有效词元时返回空串。
func (ix *Index) buildMatch(query string, mode Mode) string {
	sep := " AND "
	if mode == ModeOR {
		sep = " OR "
	}
	terms := make([]string, 0, 4)
	for _, raw := range strings.Fields(query) {
		if !hasIndexableRune(raw) {
			continue // 纯标点词元在 FTS5 里不产生 token，包成短语会触发语法错误
		}
		terms = append(terms, ix.termClause(raw))
	}
	return strings.Join(terms, sep)
}

// termClause 生成单个词元的 FTS5 条件。
//
// 别名扩展（AC1）：词元命中别名表时，展开为该组全部成员条件的 OR 组，
// 整组作为**一个**词元条件参与外层 AND/OR——保证「词元之间」的语义不被
// 别名打乱（例如 `隧道 端口` 不会因别名退化成全部 OR）。
func (ix *Index) termClause(raw string) string {
	group := ix.syn.expansions(raw)
	if len(group) == 0 {
		return plainClause(raw)
	}
	parts := make([]string, 0, len(group))
	for _, member := range group {
		if c := plainClause(member); c != "" {
			parts = append(parts, c)
		}
	}
	switch len(parts) {
	case 0:
		return plainClause(raw)
	case 1:
		return parts[0]
	default:
		return "(" + strings.Join(parts, " OR ") + ")"
	}
}

// plainClause 把一个词（无别名展开）转成 FTS5 短语条件。
//
// 当前为批次 A 行为：沿用单字切分（与入库侧 tokenizeForFTS 一致），中文按
// 相邻单字短语匹配。批次 B（T8）将改为「≥2 字 CJK 走 body_bg 二字组、
// 单字走 body 列」，以消除「生，日」被 `"生 日"` 误命中的问题。
func plainClause(raw string) string {
	if !hasIndexableRune(raw) {
		return ""
	}
	return `"` + strings.ReplaceAll(tokenizeForFTS(raw), `"`, `""`) + `"`
}

// hasIndexableRune 报告 s 是否含至少一个可检索字符（字母/数字/CJK）。
func hasIndexableRune(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
