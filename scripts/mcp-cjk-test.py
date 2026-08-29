# -*- coding: utf-8 -*-
"""通过 HTTP MCP 测试写入 + 搜索（验证 MCP 增量索引与中文检索）"""
import json
import time
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

# 1. initialize
resp, session = call(None, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                          "clientInfo": {"name": "py-test", "version": "1.0"}}, 1)
print("session:", session)

# 2. memory_write 新文件
resp, _ = call(session, "tools/call", {"name": "memory_write",
                                       "arguments": {"path": "notes/mcp-cjk-test.md",
                                                     "content": "MCP中文检索测试 独特词甲 专升本乙"}}, 2)
print("write:", resp.get("result", {}).get("content", [{}])[0].get("text", resp))

# 3. 等 watcher 索引
time.sleep(2)

# 4. 搜索中文词（MCP 层）
for q in ["独特词", "专升本", "MCP"]:
    resp, _ = call(session, "tools/call", {"name": "memory_search",
                                           "arguments": {"query": q, "limit": 3}}, 3)
    text = resp.get("result", {}).get("content", [{}])[0].get("text", str(resp))
    print(f"search[{q}]: {text[:100]}")

# 5. 清理测试文件
resp, _ = call(session, "tools/call", {"name": "memory_write",
                                       "arguments": {"path": "notes/mcp-cjk-test.md",
                                                     "content": "", "overwrite": True}}, 4)
print("cleanup:", resp.get("result", {}).get("content", [{}])[0].get("text", resp))
