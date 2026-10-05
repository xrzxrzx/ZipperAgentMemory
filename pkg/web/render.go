package web

import (
	"bytes"
	"fmt"
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
	}
	if isBinary(content) {
		return KindBinary
	}
	return KindText
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
