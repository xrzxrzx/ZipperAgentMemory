#!/bin/bash
# 诊断：MCP 客户端是否实际连过记忆库
echo "=== 1. daemon log - MCP 相关 ==="
journalctl -u zipper-agent-memory --no-pager | grep -iE "search|call|request|method" | tail -15
echo "=== 2. 当前 8931 活动连接 ==="
ss -tn state established 2>/dev/null | grep 8931 | head -5
echo "(empty = no active connection)"
echo "=== 3. 记忆库文件最近修改 ==="
ls -lt --time-style="+%Y-%m-%d %H:%M" /opt/zipper-agent-memory/memory/structured/ /opt/zipper-agent-memory/memory/notes/ 2>/dev/null | head -15
echo "=== 4. daemon 运行时长 ==="
ps -o pid,etime,cmd -p $(pgrep -f zipper-agent-memoryd | head -1) 2>/dev/null
echo "=== DONE ==="
