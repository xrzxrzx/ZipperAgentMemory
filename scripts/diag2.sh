#!/bin/bash
echo "=== 1. 全部 daemon 日志行（45 行总量）==="
journalctl -u zipper-agent-memory --no-pager 2>&1 | tail -20
echo ""
echo "=== 2. 日志中是否出现过 MCP 工具调用 ==="
journalctl -u zipper-agent-memory --no-pager 2>&1 | grep -iE "tool|call|search|write|append|read|mcp" | grep -v "daily commit\|no staged\|listening\|index ready\|allowlist\|loaded config" | head -20
echo "(empty above = no MCP tool calls logged)"
echo ""
echo "=== 3. memory/ 非 .git 文件修改时间 ==="
find /opt/zipper-agent-memory/memory -type f -not -path "*/.git/*" -printf "%TY-%Tm-%Td %TH:%TM %p\n" 2>/dev/null | sort -r
echo ""
echo "=== 4. 索引文件时间 ==="
ls -lh --time-style="+%Y-%m-%d %H:%M" /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite* 2>/dev/null
echo "=== DONE ==="
