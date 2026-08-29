# -*- coding: utf-8 -*-
"""定位 notification 400 的精确触发条件"""
import json
import urllib.request

BASE = "http://8.141.89.50:8931/mcp"

def post(session, method, params, mid=None, proto="2025-06-18"):
    msg = {"jsonrpc": "2.0", "method": method, "params": params}
    if mid is not None:
        msg["id"] = mid
    body = json.dumps(msg).encode("utf-8")
    headers = {"Content-Type": "application/json",
               "Accept": "application/json, text/event-stream",
               "MCP-Protocol-Version": proto}
    if session:
        headers["Mcp-Session-Id"] = session
    req = urllib.request.Request(BASE, data=body, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, r.read().decode("utf-8")[:120]
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8")[:200]

# 1. initialize 获取 session（2025-06-18 旧协议）
st, body = post(None, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                     "clientInfo": {"name": "ntest", "version": "1"}}, mid=1)
print(f"initialize: {st} {body[:80]}")
# 需要从响应里拿 session —— 用 header
import http.client
# 简化：再初始化一次拿 header
req = urllib.request.Request(BASE, data=json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
     "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "ntest", "version": "1"}}}).encode("utf-8"),
     headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2025-06-18"})
with urllib.request.urlopen(req, timeout=15) as r:
    sid = r.headers.get("Mcp-Session-Id")
print("session:", sid)

# 2. 带 session 的 notification（旧协议 2025-06-18）
st, body = post(sid, "notifications/initialized", {}, proto="2025-06-18")
print(f"notification(session, proto=2025-06-18): {st} {body[:120]}")

# 3. 带 session 的 notification（不带头？DSH 是否带头？）
# 用 Node 风格无 protocol 头试试
import urllib.request
body2 = json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}}).encode("utf-8")
req = urllib.request.Request(BASE, data=body2, headers={"Content-Type": "application/json",
     "Accept": "application/json, text/event-stream", "Mcp-Session-Id": sid})
try:
    with urllib.request.urlopen(req, timeout=15) as r:
        print(f"notification(session, no proto header): {r.status} {r.read().decode('utf-8')[:100]}")
except urllib.error.HTTPError as e:
    print(f"notification(session, no proto header): {e.code} {e.read().decode('utf-8')[:150]}")
