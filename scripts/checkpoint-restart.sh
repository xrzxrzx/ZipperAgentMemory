#!/bin/bash
echo "=== 1. 停服务 ==="
systemctl stop zipper-agent-memory
/bin/sleep 1
echo "=== 2. WAL checkpoint ==="
sqlite3 /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite "PRAGMA wal_checkpoint(TRUNCATE);" 2>&1
ls -lh /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite* 2>&1
echo "=== 3. 重启服务 ==="
systemctl start zipper-agent-memory
/bin/sleep 2
systemctl is-active zipper-agent-memory
echo "=== DONE ==="
