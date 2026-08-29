# -*- coding: utf-8 -*-
"""完整 MCP 会话流程测试：initialize -> tools/list -> tools/call，检查每个响应的状态码"""
import json
import urllib.request

BASE = "http://8.141.89.50:8931/mcp"

def call(session, method, params, mid):
    body = json.dumps({"jsonrpc": "2.0", "id": mid, "method": method, "params": params}).encode("utf-8")
    headers = {"Content-Type": "application/json",
               "Accept": "application/json, text/event-stream",
               "MCP-Protocol-Version": "2025-06-18"}
    if session:
        headers["Mcp-Session-Id"] = session
    req = urllib.request.Request(BASE, data=body, headers=headers)
    with urllib.request.urlopen(req, timeout=20) as resp:
        ct = resp.headers.get("Content-Type", "")
        sid = resp.headers.get("Mcp-Session-Id", "")
        data = resp.read().decode("utf-8")
        return resp.status, ct, sid, data

# 1. initialize
status, ct, sid, data = call(None, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                                  "clientInfo": {"name": "flow-test", "version": "1.0"}}, 1)
print(f"[initialize] status={status} ct={ct} session={sid}")
print(f"  body: {data[:120]}")

# 2. tools/list（带 session）
status, ct, sid2, data = call(sid, "tools/list", {}, 2)
print(f"[tools/list] status={status} ct={ct}")
print(f"  body: {data[:150]}")

# 3. tools/call memory_search
status, ct, sid3, data = call(sid, "tools/call", {"name": "memory_search", "arguments": {"query": "QQ", "limit": 1}}, 3)
print(f"[tools/call] status={status} ct={ct}")
print(f"  body: {data[:150]}")

# 4. notifications/initialized（MCP 客户端通常会发）
status, ct, sid4, data = call(sid, "notifications/initialized", {}, 4)
print(f"[notifications/initialized] status={status} ct={ct}")
print(f"  body: {data[:100]}")
