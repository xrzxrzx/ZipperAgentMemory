# -*- coding: utf-8 -*-
"""分析 Claude Code 会话中 memory/zipper 相关记录"""
import json
import io

p = r'C:\Users\xrzxr\.claude\projects\C--Users-xrzxr\005a0c65-1b4c-4e76-acdc-3752d5a06f1b.jsonl'

rows = []
with io.open(p, 'r', encoding='utf-8') as f:
    for line in f:
        try:
            obj = json.loads(line)
        except Exception:
            continue
        s = json.dumps(obj, ensure_ascii=False)
        if 'memory' in s.lower() or 'zipper' in s.lower():
            rows.append(obj)

print('total memory/zipper rows:', len(rows))
for i, obj in enumerate(rows):
    t = obj.get('type', '?')
    msg = obj.get('message', {})
    content = msg.get('content', '')
    if isinstance(content, list):
        text = ' | '.join(str(c).replace('\n', ' ')[:120] for c in content)
    else:
        text = str(content)[:120]
    name = obj.get('name', '')
    tool = ''
    if isinstance(content, list):
        for c in content:
            if isinstance(c, dict) and c.get('type') == 'tool_use':
                tool = c.get('name', '')
                inp = json.dumps(c.get('input', {}), ensure_ascii=False)[:200]
    print(f'[{i}] type={t} name={name} tool={tool}')
    if tool:
        print(f'    input: {inp}')
    else:
        print(f'    text: {text}')
