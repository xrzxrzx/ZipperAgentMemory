package web

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
	"io"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Kind 是文件内容在页面上的展示类型。
type Kind string

const (
	// KindMarkdown 按 Markdown 渲染为 HTML。
	KindMarkdown Kind = "markdown"
	// KindCSV 解析为 HTML 表格（`.csv` 逗号分隔 / `.tsv` 制表符分隔）。
	KindCSV Kind = "csv"
	// KindText 以等宽纯文本展示（由前端用 textContent 写入，不做 HTML 解析）。
	KindText Kind = "text"
	// KindBinary 二进制文件：页面不展示内容，只提供下载。
	KindBinary Kind = "binary"
	// KindLarge 超过内联展示上限：只提供下载。
	KindLarge Kind = "large"
)

// maxRenderBytes 是单文件内联展示上限（1 MiB）：超过只允许下载，
// 避免把大文件整块塞进 JSON 响应。
const maxRenderBytes = 1 << 20

// csvMaxRows 是表格渲染的最大数据行数（含作为表头的首行）。
// 超出部分不渲染，由前端提示「完整内容请下载」——HTML 体积与输入同阶，
// 加上 maxRenderBytes 上限，无需再对列数设限。
const csvMaxRows = 5000

// markdown 渲染器：启用 GFM 扩展（表格、任务列表、自动链接等）。
//
// 刻意不启用 html.WithUnsafe()——goldmark 默认**不渲染**正文里的原始 HTML
// （替换为 `<!-- raw HTML omitted -->` 注释标记），而记忆库中存在从外部抓取的
// 资料，若直渲染会让其中的 <script> 执行。注意语义是「省略」而非「转义为实体」，
// 两者都不可执行，但页面展示效果不同。
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// KindOf 依据扩展名与内容判定展示类型。
func KindOf(rel string, content []byte) Kind {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".markdown":
		return KindMarkdown
	case ".csv", ".tsv":
		return KindCSV // 分隔符由 csvDelimiter 按扩展名决定
	}
	if isBinary(content) {
		return KindBinary
	}
	return KindText
}

// csvDelimiter 返回按扩展名选定的分隔符：`.tsv` 用制表符，其余（`.csv`）用逗号。
func csvDelimiter(rel string) rune {
	if strings.EqualFold(path.Ext(rel), ".tsv") {
		return '\t'
	}
	return ','
}

// isBinary 以内容判定二进制：开头 512 字节内出现 NUL 即视为二进制。
func isBinary(content []byte) bool {
	n := len(content)
	if n > 512 {
		n = 512
	}
	return bytes.IndexByte(content[:n], 0) >= 0
}

// RenderMarkdown 把 Markdown 渲染为 HTML 片段（原始 HTML 被转义）。
func RenderMarkdown(content []byte) (string, error) {
	var buf bytes.Buffer
	if err := markdown.Convert(content, &buf); err != nil {
		return "", fmt.Errorf("web: render markdown: %w", err)
	}
	return buf.String(), nil
}

// RenderCSV 把分隔符文本解析为 HTML 表格，返回 (表格 HTML, 是否被行数截断, 错误)。
//
// 为什么用标准库 encoding/csv 而不是按分隔符切分：CSV 存在引号转义、引号内
// 逗号、引号内换行等情形，手写切分必然出错。
//
// 解析策略（面向"展示"而非"严格校验"）：
//   - 剥掉 UTF-8 BOM（Windows 记事本等编辑器产物）；
//   - FieldsPerRecord=-1：容忍列数不齐的行（库内 CSV 常有不规则行）；
//   - LazyQuotes=true：容忍不规范引号，按字面处理而不是整份报错；
//   - 首行作为 <thead>，其余为 <tbody>；
//   - **每个单元格经 html.EscapeString 转义**——库内 CSV 同样可能含尖括号。
//     注意该函数对双引号输出数字实体 `&#34;`（`&amp;`/`&lt;`/`&gt;`/`&#39;` 同理），
//     与 `&quot;` 等价，浏览器一视同仁；
//   - 超出 maxRows 行即停止并置 truncated（由调用方提示下载）。
//
// 返回的 HTML 交给前端以 innerHTML 注入，安全性由"逐单元格转义"保证。
func RenderCSV(content []byte, comma rune, maxRows int) (string, bool, error) {
	content = bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})
	r := csv.NewReader(bytes.NewReader(content))
	r.Comma = comma
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	var b strings.Builder
	b.WriteString(`<table class="csv">`)
	rows := 0
	truncated := false
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, fmt.Errorf("web: parse csv: %w", err)
		}
		if rows >= maxRows {
			truncated = true
			break
		}
		if rows == 0 {
			b.WriteString("<thead><tr>")
			for _, cell := range rec {
				fmt.Fprintf(&b, "<th>%s</th>", html.EscapeString(cell))
			}
			b.WriteString("</tr></thead><tbody>")
		} else {
			b.WriteString("<tr>")
			for _, cell := range rec {
				fmt.Fprintf(&b, "<td>%s</td>", html.EscapeString(cell))
			}
			b.WriteString("</tr>")
		}
		rows++
	}
	b.WriteString("</tbody></table>")
	return b.String(), truncated, nil
}
