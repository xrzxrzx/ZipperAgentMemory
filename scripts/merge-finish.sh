#!/bin/bash
# 合并后处理：重建索引 + 提交 + 推送
export PATH=/usr/local/go/bin:$PATH
cd /opt/zipper-agent-memory

echo "=== 1. 重建索引 ==="
./zipper-agent-memoryd rebuild-index -root memory 2>&1
echo "=== 2. 搜索验证（学习内容是否可搜）==="
./zipper-agent-memoryd search -root memory "专升本" 2>&1 | head -5
echo "=== 3. 主动提交 ==="
./zipper-agent-memoryd git-commit -root memory 2>&1 | tail -2
echo "=== 4. 推送 Gitee ==="
cd memory && git push gitee master 2>&1 | tail -2
echo "=== 5. 远端历史 ==="
git log gitee/master --oneline -4
echo "=== DONE ==="
