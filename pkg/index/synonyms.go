package index

import (
	"os"
	"strings"
	"sync"
	"time"
)

// synonymsMaxPerGroup 是别名表单组成员数上限（含正词）。超出则截断，
// 防止一行写过长导致查询表达式爆炸（技术方案.md §3.1）。
const synonymsMaxPerGroup = 10

// synonymTable 是别名表的只读快照：每个成员都映射到其所属组的全部成员，
// 实现组内对称扩展（搜任一成员命中整组）。
type synonymTable struct {
	byMember map[string][]string
}

// lookup 返回 term 所属组的全部成员（含 term 自身）；未登记时返回 nil。
// 允许 nil receiver：别名表不可用时一律退化为「无扩展」。
func (t *synonymTable) lookup(term string) []string {
	if t == nil {
		return nil
	}
	return t.byMember[term]
}

// size 报告已登记成员数（供日志/诊断）。
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
//   - 空行与 `#` 起始行忽略；
//   - 制表符分隔，首列为正词，其余为别名；
//   - 有效成员数 < 2 的行视为坏行跳过（AC2：不报错、不 panic）；
//   - 单组成员数上限 synonymsMaxPerGroup，超出截断；
//   - 同一成员在多行出现时，以先出现的行为准（首行优先）。
func parseSynonyms(text string) *synonymTable {
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
			if f = strings.TrimSpace(f); f != "" {
				members = append(members, f)
			}
		}
		if len(members) < 2 {
			continue // 坏行：没有别名可用
		}
		if len(members) > synonymsMaxPerGroup {
			members = members[:synonymsMaxPerGroup]
		}
		for _, m := range members {
			if _, exists := byMember[m]; !exists {
				byMember[m] = members
			}
		}
	}
	return &synonymTable{byMember: byMember}
}
