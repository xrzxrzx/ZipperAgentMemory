package index

import (
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

// synonymsMaxPerGroup 是别名表单组成员数上限（含正词）。超出则截断，
// 防止一行写过长导致查询表达式爆炸（技术方案.md §3.1）。
const synonymsMaxPerGroup = 10

// synonymsBOM 是 UTF-8 BOM。PowerShell 5.1 的 `Out-File -Encoding utf8`
// 默认写入 BOM，必须容忍，否则整张别名表会静默失效（独立验证 P2-6）。
const synonymsBOM = "\uFEFF"

// synonymTable 是别名表的只读快照：每个成员都映射到其所属组的全部成员，
// 实现组内对称扩展（搜任一成员命中整组）。
type synonymTable struct {
	byMember map[string][]string
}

// lookup 返回 term 所属组的全部成员（含 term 自身）；未登记时返回 nil。
// 允许 nil receiver：别名表不可用时一律退化为「无扩展」。
//
// 查找大小写不敏感（先精确、再小写回退），与 pathMatches 的 ToLower 口径一致
// （独立验证 P2-4）。
func (t *synonymTable) lookup(term string) []string {
	if t == nil {
		return nil
	}
	if v, ok := t.byMember[term]; ok {
		return v
	}
	return t.byMember[strings.ToLower(term)]
}

// size 报告已登记键数（含小写别名键，仅供诊断）。
func (t *synonymTable) size() int {
	if t == nil {
		return 0
	}
	return len(t.byMember)
}

// synonymCache 按 (mtime, size) 缓存别名表，避免每次查询都重读文件。
//
// 设计取舍：查询时 stat 一次（开销极小、无需 watcher 联动），文件被修改后
// 下一次查询即生效（需求.md §4：改文件无需重建索引）。
type synonymCache struct {
	mu     sync.Mutex
	path   string
	mtime  time.Time
	size   int64
	loaded bool
	table  *synonymTable
}

// expansions 返回 term 的别名扩展（含 term 自身）；未启用或查不到时返回 nil。
func (c *synonymCache) expansions(term string) []string {
	return c.load().lookup(term)
}

// load 返回当前应使用的别名表（可能为 nil）。
//
// 降级语义（AC2）：文件不存在、读失败、格式全坏 —— 一律退化为 nil，
// 即检索行为与未启用该特性时完全一致，不报错、不 panic。
func (c *synonymCache) load() *synonymTable {
	if c == nil || c.path == "" {
		return nil
	}
	info, err := os.Stat(c.path)
	if err != nil {
		// 文件消失：清缓存，立即退化为「无别名表」，避免继续用陈旧表。
		c.mu.Lock()
		c.loaded = false
		c.table = nil
		c.mu.Unlock()
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded && info.ModTime().Equal(c.mtime) && info.Size() == c.size {
		return c.table
	}

	data, err := os.ReadFile(c.path)
	if err != nil {
		// 读失败（权限/占用等）：保留旧表（若有），不打断检索。
		return c.table
	}
	c.table = parseSynonyms(string(data))
	c.mtime = info.ModTime()
	c.size = info.Size()
	c.loaded = true
	return c.table
}

// parseSynonyms 解析别名表文本（格式见 技术方案.md §3.1）：
//
//   - 开头允许 UTF-8 BOM（PS 5.1 常见）；
//   - 空行与 `#` 起始行忽略；
//   - 制表符分隔，首列为正词，其余为别名；
//   - **含控制字符（含 NUL）的成员一律丢弃**——它们会破坏 FTS5 MATCH 表达式，
//     甚至让 SQLite 报 "unterminated string" 并连带让无关查询失败（AC2、P1-2）；
//   - 有效成员数 < 2 的行视为坏行跳过（不报错、不 panic）；
//   - 单组成员数上限 synonymsMaxPerGroup，超出截断；
//   - 同一成员在多行出现时，以先出现的行为准（首行优先）；
//   - 同时登记小写键，使英文别名查找大小写不敏感（P2-4）。
func parseSynonyms(text string) *synonymTable {
	text = strings.TrimPrefix(text, synonymsBOM)
	byMember := make(map[string][]string)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		members := make([]string, 0, len(fields))
		for _, f := range fields {
			f = strings.TrimSpace(f)
			if f == "" || strings.ContainsFunc(f, unicode.IsControl) {
				continue
			}
			members = append(members, f)
		}
		if len(members) < 2 {
			continue // 坏行：没有可用的别名
		}
		if len(members) > synonymsMaxPerGroup {
			members = members[:synonymsMaxPerGroup]
		}
		for _, m := range members {
			register := func(key string) {
				if _, exists := byMember[key]; !exists {
					byMember[key] = members
				}
			}
			register(m)
			register(strings.ToLower(m))
		}
	}
	return &synonymTable{byMember: byMember}
}
