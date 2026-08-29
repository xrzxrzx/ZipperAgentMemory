#!/bin/bash
echo "=== 1. serve 进程的启动参数 ==="
ps aux | grep "zipper-agent-memoryd serve" | grep -v grep | head -2
echo "=== 2. systemd 单元 ExecStart ==="
grep ExecStart /etc/systemd/system/zipper-agent-memory.service
echo "=== 3. 当前工作目录下的索引文件 ==="
ls -lh /opt/zipper-agent-memory/.zipper-agent-memory.index.sqlite* 2>&1
echo "=== 4. 是否有第二个索引文件（serve 可能建在别处）==="
find /opt/zipper-agent-memory -name "*.sqlite*" 2>/dev/null
echo "=== 5. serve 日志中的 db 路径 ==="
journalctl -u zipper-agent-memory --no-pager | grep "index ready" | tail -1
echo "=== DONE ==="
