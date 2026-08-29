#!/bin/bash
# 用应用层 CLI 测试中文检索（走 tokenizeForFTS）
export PATH=/usr/local/go/bin:$PATH
cd /opt/zipper-agent-memory
echo "=== CLI 搜索 密码 ==="
./zipper-agent-memoryd search -root memory "密码" 2>&1 | head -4
echo "=== CLI 搜索 密 码（空格）==="
./zipper-agent-memoryd search -root memory "密 码" 2>&1 | head -4
echo "=== CLI 搜索 专升本 ==="
./zipper-agent-memoryd search -root memory "专升本" 2>&1 | head -4
echo "=== CLI 搜索 学习 ==="
./zipper-agent-memoryd search -root memory "学习" 2>&1 | head -4
echo "=== DONE ==="
