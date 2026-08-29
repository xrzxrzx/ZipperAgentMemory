# -*- coding: utf-8 -*-
"""测试不同 MCP 协议版本握手（找 DSH 用的版本是否被服务器接受）"""
import json
import urllib.request

BASE = "http://8.141.89.50:8931/mcp"
versions = ["2025-11-25", "2025-06-18", "2024-11-05", "2025-03-26"]

for v in versions:
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                       "params": {"protocolVersion": v, "capabilities": {},
                                  "clientInfo": {"name": "proto-test", "version": "1.0"}}}).encode("utf-8")
    headers = {"Content-Type": "application/json",
               "Accept": "application/json, text/event-stream",
               "MCP-Protocol-Version": v}
    req = urllib.request.Request(BASE, data=body, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            data = resp.read().decode("utf-8")
            ok = '"result"' in data
            print(f"protocol {v}: HTTP {resp.status}, result={ok}")
            if not ok:
                print(f"    body: {data[:150]}")
    except Exception as e:
        print(f"protocol {v}: FAILED - {e}")
