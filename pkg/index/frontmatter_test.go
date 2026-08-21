package index

import (
	"strings"
	"testing"
)

func TestParseFrontMatter(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantTags  string
		wantBody  string
		wantIsRaw bool // 期望原样返回（无 front-matter）
	}{
		{
			name:     "完整 front-matter（tags 内联列表）",
			content:  "---\ntags: [go, dev]\ncreated: 2026-08-01\nsource: manual\n---\n正文第一行\n正文第二行",
			wantTags: "go dev",
			wantBody: "正文第一行\n正文第二行",
		},
		{
			name:     "tags 逗号标量",
			content:  "---\ntags: go, dev\n---\nbody",
			wantTags: "go dev",
			wantBody: "body",
		},
		{
			name:     "CRLF 换行",
			content:  "---\r\ntags: a\r\n---\r\nbody\r\n",
			wantTags: "a",
			wantBody: "body\r\n",
		},
		{
			name:      "无 front-matter",
			content:   "plain text 没有横线开头",
			wantIsRaw: true,
		},
		{
			name:      "有开头无闭合",
			content:   "---\ntags: x\n正文混入",
			wantIsRaw: true,
		},
		{
			name:      "空内容",
			content:   "",
			wantIsRaw: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, body := ParseFrontMatter([]byte(tt.content))
			if tt.wantIsRaw {
				if body != tt.content || m.Tags != "" {
					t.Errorf("raw case: body=%q tags=%q", body, m.Tags)
				}
				return
			}
			if m.Tags != tt.wantTags {
				t.Errorf("Tags = %q, want %q", m.Tags, tt.wantTags)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestShouldIndex(t *testing.T) {
	tests := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"notes/go.md", false, true},
		{"README.md", false, true},
		{"structured/tasks.csv", false, true},
		{"notes/a.markdown", false, true},
		{"notes/a.txt", false, true},
		{"notes/a.yaml", false, true},
		{"notes/a.toml", false, true},
		{"notes/a.json", false, true},
		{"notes/a.tsv", false, true},
		{"notes/a.bin", false, false},
		{"notes/a.png", false, false},
		{"notes/a.pdf", false, false},
		{".git/config", false, false},      // 隐藏目录内文件
		{".zipper-index", false, false},    // 隐藏路径
		{"notes/.tmp-x", false, false},     // 原子写遗留临时文件
		{"notes/.hidden.md", false, false}, // 隐藏文件
		{"notes", true, true},              // 目录可索引（遍历剪枝依据）
		{".git", true, false},              // 隐藏目录剪枝
		{"", false, false},                 // 根目录本身不索引
		{".", false, false},                // 根目录本身不索引
		{"notes/UPPER.MD", false, true},    // 扩展名大小写不敏感
	}
	for _, tt := range tests {
		if got := ShouldIndex(tt.rel, tt.isDir); got != tt.want {
			t.Errorf("ShouldIndex(%q, %v) = %v, want %v", tt.rel, tt.isDir, got, tt.want)
		}
	}
}

func TestTokenizeForFTS(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"语言学习", " 语 言 学 习 "},
		{"Go语言开发", "Go 语 言 开 发 "},
		{"abc", "abc"},
		{"", ""},
		{"内存", " 内 存 "},
		{"a汉语b", "a 汉 语 b"},
	}
	for _, tt := range tests {
		if got := tokenizeForFTS(tt.in); got != tt.want {
			t.Errorf("tokenizeForFTS(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUntokenizeSnippet(t *testing.T) {
	tests := []struct{ in, want string }{
		{"[ 李 白 ]", "[ 李白 ]"},
		{"Go 语 言 学 习", "Go 语言学习"},
		{"plain text", "plain text"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := untokenizeSnippet(tt.in); got != tt.want {
			t.Errorf("untokenizeSnippet(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// 与 tokenize 互逆性抽查：纯中文串 token 化后还原应得到原串（忽略首尾补的空格）。
	raw := "李白开发"
	if got := untokenizeSnippet(tokenizeForFTS(raw)); strings.TrimSpace(got) != raw {
		t.Errorf("roundtrip = %q, want %q", got, raw)
	}
}

// TestFrontMatterBodyNotDoubleIndexed 验证正文里不再含 front-matter 头
// （ParseFrontMatter 输出的 body 不应以 "---" 开头）。
func TestFrontMatterBodyNotDoubleIndexed(t *testing.T) {
	_, body := ParseFrontMatter([]byte("---\ntags: x\n---\n正文"))
	if strings.HasPrefix(body, "---") {
		t.Errorf("body should not include front-matter header: %q", body)
	}
}
