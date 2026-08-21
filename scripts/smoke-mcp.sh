#!/bin/bash
# MCP 冒烟测试脚本（服务器端执行）
set -e
BASE="http://127.0.0.1:8931/mcp"

echo "=== 1. initialize ==="
INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1.0"}}}'
RESP=$(curl -s -D /tmp/headers.txt -X POST "$BASE" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d "$INIT")
echo "$RESP" | head -c 300
echo
SESSION=$(grep -i "mcp-session-id" /tmp/headers.txt | tr -d '\r' | awk '{print $2}')
echo "session: $SESSION"

echo "=== 2. tools/list ==="
curl -s -X POST "$BASE" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" ${SESSION:+-H "Mcp-Session-Id: $SESSION"} \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | head -c 800
echo

echo "=== 3. tools/call memory_search ==="
curl -s -X POST "$BASE" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" ${SESSION:+-H "Mcp-Session-Id: $SESSION"} \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"P站","limit":3}}}' | head -c 600
echo
echo "=== SMOKE DONE ==="
