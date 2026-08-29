# -*- coding: utf-8 -*-
"""模拟 streamable-http 完整握手（initialize + 读取 SSE 响应流）"""
import json
import urllib.request

BASE = "http://8.141.89.50:8931/mcp"

# streamable-http 握手：POST initialize，应返回 JSON 或 SSE 流
body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                   "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                              "clientInfo": {"name": "sse-test", "version": "1.0"}}}).encode("utf-8")
headers = {
    "Content-Type": "application/json",
    "Accept": "application/json, text/event-stream",
    "MCP-Protocol-Version": "2025-06-18",
}

req = urllib.request.Request(BASE, data=body, headers=headers)
try:
    with urllib.request.urlopen(req, timeout=20) as resp:
        print("status:", resp.status)
        print("content-type:", resp.headers.get("Content-Type"))
        print("mcp-session-id:", resp.headers.get("Mcp-Session-Id"))
        data = resp.read().decode("utf-8")
        print("body head:", data[:300])
except Exception as e:
    print("FAILED:", e)
    try:
        print("body:", e.read().decode("utf-8")[:300])
    except Exception:
        pass
