#!/bin/bash
echo "=== 1. daemon 日志（watcher/index 事件 18:38 后）==="
journalctl -u zipper-agent-memory --no-pager --since "2026-08-29 18:38" 2>&1 | tail -15
echo "=== 2. 索引表内容 ==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT COUNT(*) FROM docs;" 2>&1
echo "=== 3. 索引中的文件路径 ==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT path FROM docs ORDER BY path;" 2>&1
echo "=== 4. FTS 匹配测试（学习规划）==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT path FROM docs_fts WHERE docs_fts MATCH '专升本' LIMIT 3;" 2>&1
echo "=== DONE ==="
