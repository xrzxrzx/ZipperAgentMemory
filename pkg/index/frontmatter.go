package index

import (
	"strings"
	"unicode"
)

// ParseFrontMatter 提取内容开头的 YAML front-matter 元数据（tags/created/source）。
//
// 语法子集（不引入第三方 YAML 依赖，见编码规范 §7.1）：
//   - 文件首行必须恰为 "---"，其后每行一个 "key: value" 字段，遇到 "---" 或 "..." 结束；
//   - 只识别 tags / created / source 三个键，其余键忽略；
//   - tags 支持标量（"go, dev"）与内联列表（"[go, dev]"）两种写法，统一规范化为
//     空格分隔的字符串（供 FTS 按词检索）；
//   - 无有效 front-matter（首行不是 "---" 或无闭合标记）时返回空 Meta 与完整原文。
//
// 返回的 Meta 仅填充 Tags/Created/Source；MTime/Size 由调用方从文件 stat 填充。
// 正文去掉 front-matter 块并裁掉其后的一个换行。
func ParseFrontMatter(content []byte) (Meta, string) {
	lines := strings.Split(string(content), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return Meta{}, string(content)
	}
	var m Meta
	i := 1
	closed := false
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" || line == "..." {
			closed = true
			i++
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue // 空行与注释跳过
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "tags":
			m.Tags = normalizeTags(val)
		case "created":
			m.Created = strings.TrimSpace(val)
		case "source":
			m.Source = strings.TrimSpace(val)
		}
	}
	if !closed {
		return Meta{}, string(content) // 有 "---" 开头但无闭合，按无 front-matter 处理
	}
	body := strings.TrimPrefix(strings.Join(lines[i:], "\n"), "\n")
	return m, body
}

// normalizeTags 把 tags 字段规范化为空格分隔的词列表：
// 去两侧空白与包裹括号，按逗号切分并逐项 trim，再以空格连接。
// 例如 "[go, dev]"、"go, dev"、"go dev" 均归一为 "go dev"。
func normalizeTags(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "[]()")
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, " ")
}

// isCJKRune 报告 r 是否属于 CJK 文字（汉字/假名/谚文）。
// 这些字符在 unicode61 分词器下会被并入同一 token，必须单字切分才能检索。
func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// tokenizeForFTS 把待入库/待查询文本做 FTS5 兼容切分：每个 CJK 字符前后补空格，
// 使 unicode61 分词器把每个汉字/假名/谚文字符拆成独立 token（非 CJK 部分原样保留，
// 已存在的空白不会被叠加成双空格）。
//
// 效果：正文 "语言学习" 入库为 tokens [语][言][学][习]；查询 "语言" 切成短语
// [语][言]，按 FTS5 短语连续匹配命中相邻 token，中文短语检索成立；
// 单字查询同样成立。查询侧与入库侧必须使用同一函数，保证两端 token 一致。
func tokenizeForFTS(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	space := func() {
		if b.Len() == 0 || b.String()[b.Len()-1] != ' ' {
			b.WriteByte(' ')
		}
	}
	for _, r := range s {
		if isCJKRune(r) {
			space()
			b.WriteRune(r)
			space()
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// untokenizeSnippet 还原 snippet 中被 tokenizeForFTS 拆开的 CJK 空格：
// 整段空格串若左右两侧都是 CJK 字符则删除（"[ 李 白 ]" → "[ 李白 ]"），
// 让命中片段保持人类可读的连续中文。
func untokenizeSnippet(s string) string {
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); i++ {
		if runes[i] != ' ' {
			b.WriteRune(runes[i])
			continue
		}
		j := i
		for j < len(runes) && runes[j] == ' ' {
			j++
		}
		leftCJK := i > 0 && isCJKRune(runes[i-1])
		rightCJK := j < len(runes) && isCJKRune(runes[j])
		if !(leftCJK && rightCJK) {
			b.WriteString(strings.Repeat(" ", j-i))
		}
		i = j - 1
	}
	return b.String()
}
