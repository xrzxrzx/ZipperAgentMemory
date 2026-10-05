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
// （每个 CJK 字符前后补空格）。
//
// 查询语义（2026-10-05 起，见 docs/features/需求-001-记忆阅读页/）：查询串先按
// 空白切成若干词元，每词元包成双引号短语，再按 Mode 以 AND / OR 连接——
// AND（默认）要求全部词元命中（"隧道 端口" = 两词都出现），OR 命中任一即可；
// 不含可检索字符的词元被丢弃，全部被丢弃时返回 ErrEmptyQuery。
// 旧实现把整串包成单个短语，导致多关键词反而更难搜到（已修复）。
//
// 包不依赖文件监听（pkg/watch 负责）；两者由 cmd/zipper-agent-memoryd 编排。
package index
