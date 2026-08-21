// Package index 实现记忆库的 SQLite FTS5 全文索引（阶段 2）。
//
// 索引是 derived state（设计文档 §9 R7）：文件是唯一真源，索引可随时
// 通过 Rebuild 全量重建，任何不一致都不需要加锁强一致保证。
//
// 架构：单一 SQLite 文件（WAL 模式），两张表——
//   - docs：path（唯一键）、mtime、size、front-matter 元数据（tags/created/source）；
//   - docs_fts：FTS5 虚拟表，存元数据 + 正文，供全文检索；
//     docs 与 docs_fts 通过 rowid 关联，Upsert/Remove 同步维护两表。
//
// 中文检索：FTS5 默认 unicode61 分词器把连续汉字当作单一 token（"语言学习"
// 不可被 "语言" 命中），故入库与查询两侧都经 tokenizeForFTS 做单字切分
// （每个 CJK 字符前后补空格），查询以双引号短语形式提交，保证无语法错误。
//
// 包不依赖文件监听（pkg/watch 负责）；两者由 cmd/zipper-agent-memoryd 编排。
package index
