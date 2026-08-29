# -*- coding: utf-8 -*-
"""最终验证：MCP 搜索学习记忆（之前 PowerShell 测试失败的词）"""
import json
import urllib.request

BASE = "http://8.141.89.50:8931/mcp"

def call(session, method, params, msg_id):
    body = json.dumps({"jsonrpc": "2.0", "id": msg_id, "method": method, "params": params}).encode("utf-8")
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if session:
        headers["Mcp-Session-Id"] = session
    req = urllib.request.Request(BASE, data=body, headers=headers)
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.loads(resp.read().decode("utf-8")), resp.headers.get("Mcp-Session-Id")

resp, session = call(None, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                          "clientInfo": {"name": "final-verify", "version": "1.0"}}, 1)

tests = ["专升本", "密码", "学习", "Anki", "摸底", "QQ", "服务器", "头插法"]
for q in tests:
    resp, _ = call(session, "tools/call", {"name": "memory_search",
                                           "arguments": {"query": q, "limit": 2}}, 2)
    text = resp.get("result", {}).get("content", [{}])[0].get("text", str(resp))
    first_line = text.split("\n")[0] if text else "(empty)"
    print(f"[{q}] -> {first_line}")
