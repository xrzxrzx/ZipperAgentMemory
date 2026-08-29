#!/bin/bash
echo "=== 1. FTS 中 password-vault.csv 的行 ==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT rowid, path, snippet(docs_fts, 3, '[', ']', '...', 50) FROM docs_fts WHERE path='structured/password-vault.csv' LIMIT 2;" 2>&1
echo "=== 2. 用 FTS MATCH 分别测 QQ / 密码 / 专升本 ==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT 'QQ:', COUNT(*) FROM docs_fts WHERE docs_fts MATCH 'QQ';" 2>&1
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT 'mi-ma:', COUNT(*) FROM docs_fts WHERE docs_fts MATCH '密码';" 2>&1
echo "=== 3. docs 表 vs docs_fts 行数 ==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT (SELECT COUNT(*) FROM docs) as docs, (SELECT COUNT(*) FROM docs_fts) as fts;" 2>&1
echo "=== 4. 密码 分词验证（tokenize 后的字节）==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "SELECT length(body) FROM docs WHERE path='structured/password-vault.csv';" 2>&1
echo "=== DONE ==="
