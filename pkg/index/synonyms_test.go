package index

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestParseSynonyms 覆盖 AC1/AC2 的解析语义：注释与空行忽略、坏行跳过、
// 组内对称扩展、未登记词返回 nil。
func TestParseSynonyms(t *testing.T) {
	text := "# 注释行应被忽略\n" +
		"\n" +
		"密码\tpassword\tpwd\n" +
		"服务器\tserver\n" +
		"孤立词\n" + // 坏行：只有一列
		"坏行\t\n" + // 坏行：别名列为空白
		"隧道\ttunnel\n"
	tab := parseSynonyms(text)

	want := []string{"密码", "password", "pwd"}
	for _, member := range want {
		if got := tab.lookup(member); !sameStrings(got, want) {
			t.Errorf("lookup(%q) = %v, want %v（对称扩展）", member, got, want)
		}
	}
	if got := tab.lookup("server"); len(got) != 2 {
		t.Errorf("lookup(server) = %v, want 2 个成员", got)
	}
	if got := tab.lookup("孤立词"); got != nil {
		t.Errorf("坏行不应登记成员，实际 %v", got)
	}
	if got := tab.lookup("坏行"); got != nil {
		t.Errorf("坏行不应登记成员，实际 %v", got)
	}
	if got := tab.lookup("从未出现"); got != nil {
		t.Errorf("未登记词应返回 nil，实际 %v", got)
	}
}

// TestParseSynonymsTruncatesLongGroup 单组成员超过上限时截断，且被截断成员不登记。
func TestParseSynonymsTruncatesLongGroup(t *testing.T) {
	var line strings.Builder
	line.WriteString("a0")
	for i := 1; i <= 20; i++ {
		line.WriteString("\t")
		line.WriteString("a" + strconv.Itoa(i))
	}
	tab := parseSynonyms(line.String())
	if got := tab.lookup("a0"); len(got) != synonymsMaxPerGroup {
		t.Fatalf("组内成员应截断到 %d，实际 %d", synonymsMaxPerGroup, len(got))
	}
	if got := tab.lookup("a15"); got != nil {
		t.Errorf("被截断的成员不应登记，实际 %v", got)
	}
}

// TestSynonymCacheMissingFile 缺文件时退化为「无别名表」，不报错不 panic（AC2）。
func TestSynonymCacheMissingFile(t *testing.T) {
	c := &synonymCache{path: filepath.Join(t.TempDir(), "nope.tsv")}
	if got := c.expansions("密码"); got != nil {
		t.Fatalf("缺文件时应返回 nil，实际 %v", got)
	}
	// 未配置路径（nil / 空路径）同样退化为 nil。
	if got := (*synonymCache)(nil).expansions("密码"); got != nil {
		t.Fatalf("nil 缓存应返回 nil，实际 %v", got)
	}
	if got := (&synonymCache{}).expansions("密码"); got != nil {
		t.Fatalf("空路径应返回 nil，实际 %v", got)
	}
}

// TestSynonymCacheFollowsFile 文件改动后下一次查询即生效（无需重建索引）；
// 文件被删除后立即退化为无别名表。
func TestSynonymCacheFollowsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synonyms.tsv")
	if err := os.WriteFile(path, []byte("密码\tpassword\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &synonymCache{path: path}
	if got := c.expansions("password"); len(got) != 2 {
		t.Fatalf("初始加载失败：%v", got)
	}

	// 换成更长内容（size 与 mtime 均变化），下一次查询应看到新表。
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(path, []byte("密码\tpasscode\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.expansions("passcode"); len(got) != 2 {
		t.Fatalf("改文件后未生效：%v", got)
	}
	if got := c.expansions("password"); got != nil {
		t.Errorf("旧别名不应仍生效，实际 %v", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := c.expansions("密码"); got != nil {
		t.Fatalf("删除文件后应退化为 nil，实际 %v", got)
	}
}

// TestParseSynonymsRejectsControlChars（AC2 / P1-2）含控制字符（尤其 NUL）的成员
// 必须被丢弃——它们会破坏 FTS5 MATCH 表达式，让**无关**查询报
// "unterminated string" 并连带让 web 出口 500。
func TestParseSynonymsRejectsControlChars(t *testing.T) {
	tab := parseSynonyms("空\x00字节\tnul\n正常组\tnormal\n")
	if got := tab.lookup("nul"); got != nil {
		t.Errorf("含 NUL 的行应整行丢弃，实际登记了 %v", got)
	}
	if got := tab.lookup("normal"); len(got) != 2 {
		t.Errorf("正常行不应受影响，实际 %v", got)
	}
}

// TestParseSynonymsToleratesBOM（P2-6）带 UTF-8 BOM 的别名表应正常加载
// —— PowerShell 5.1 的 `Out-File -Encoding utf8` 默认写 BOM。
func TestParseSynonymsToleratesBOM(t *testing.T) {
	tab := parseSynonyms("\uFEFF密码\tpassword\n")
	if got := tab.lookup("password"); len(got) != 2 {
		t.Fatalf("带 BOM 的表应正常加载，实际 %v", got)
	}
}

// TestSynonymLookupCaseInsensitive（P2-4）英文别名查找大小写不敏感。
func TestSynonymLookupCaseInsensitive(t *testing.T) {
	tab := parseSynonyms("密码\tpassword\n")
	for _, q := range []string{"password", "PASSWORD", "Password"} {
		if got := tab.lookup(q); len(got) != 2 {
			t.Errorf("lookup(%q) 应大小写不敏感，实际 %v", q, got)
		}
	}
}
