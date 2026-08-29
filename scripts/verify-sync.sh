#!/bin/bash
# 端到端验证：写入记忆 -> 提交 -> 推送 Gitee
set -e
export PATH=/usr/local/go/bin:$PATH
cd /opt/zipper-agent-memory

echo "=== 1. 模拟 AI 写入一条记忆 ==="
cat >> memory/notes/go-mcp-development.md << 'EOF'

- 2026-08-29 同步链路测试：Claude/Codex 配置验证完成；Codex mcp_servers 曾丢失现已修复；本行为端到端验证写入
EOF
tail -3 memory/notes/go-mcp-development.md

echo "=== 2. 主动提交 ==="
./zipper-agent-memoryd git-commit -root memory 2>&1 | tail -2

echo "=== 3. 推送到 Gitee ==="
cd memory
git push gitee master 2>&1 | tail -2

echo "=== 4. 验证远端 ==="
git log gitee/master --oneline -3

echo "=== DONE ==="
