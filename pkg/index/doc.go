// Package index 实现记忆库的 SQLite FTS5 全文索引（阶段 2）。
//
// 索引是 derived state（设计文档 §9 R7）：文件是唯一真源，索引可随时
// 通过 Rebuild 全量重建，任何不一致都不需要加锁强一致保证。
//
// 架构：单一 SQLite 文件（WAL 模式），两张表——
//   - docs：path（唯一键）、mtime、size、front-matter 元数据（tags/created/source）；
//   - docs_fts：FTS5 虚拟表，存元数据 + 正文（body 单字切分、body_bg 二字组），
//     供全文检索；docs 与 docs_fts 通过 rowid 关联，Upsert/Remove 同步维护两表。
//
// 中文检索（2026-10-18 起，见 docs/features/需求-005-检索质量优化/）：
// FTS5 默认 unicode61 分词器把连续汉字当作单一 token（"语言学习"不可被"语言"
// 直接命中），因此：
//   - body 列经 tokenizeForFTS 做**单字切分**（每个 CJK 字符前后补空格），
//     保证单字查询可用；
//   - body_bg 列经 tokenizeBigramForFTS 存**相邻二字组**（标点/空白处断开），
//     使「生，日」这类被隔开的两个字不再被当作连续词命中；
//   - tags/created/source 经 tokenizeMetaForFTS 做单字 + 二字组混写，
//     让元数据（尤其 tags）里的中文也能被 ≥2 字查询命中。
//
// 查询构造：查询串按空白切成词元；每个词元按 CJK / 非 CJK 分段——
// ≥2 字 CJK 走相邻二字组（AND 连接，**绝不回落单字短语**，否则 AC6 的精确率
// 目标不成立），单字走单字，非 CJK 原样；词元之间按 Mode 以 AND / OR 连接。
// 条件**不限定列名**：二字组 token 与单字 token 天然不混淆。
// 不含可检索字符的词元被丢弃，全部被丢弃时返回 ErrEmptyQuery。
//
// 别名扩展（可选）：Open 时传 WithSynonyms(path) 启用 memory/meta/synonyms.tsv，
// 组内对称扩展——搜任一成员命中整组；文件缺失/损坏/坏行一律静默降级为
// 「无别名表」，检索行为与未启用时一致。
//
// 结果排序：路径命中查询词元的行优先，其次按 bm25 取反的相关性分数降序；
// Result.Score 仅在**同一次查询内**可比较。
//
// 索引版本：indexSchemaVersion 表示内容格式版本（切分方式 / 列集合 / 列序）。
// Open 时与 PRAGMA user_version 比对，不一致则 Search 与 Upsert 返回
// ErrIndexStale，须执行 rebuild-index —— 绝不静默使用旧索引出结果。
//
// 包不依赖文件监听（pkg/watch 负责）；两者由 cmd/zipper-agent-memoryd 编排。
package index
