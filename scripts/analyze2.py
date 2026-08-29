# -*- coding: utf-8 -*-
"""精确分析 Claude Code 会话中的工具调用"""
import json
import io
import re

p = r'C:\Users\xrzxr\.claude\projects\C--Users-xrzxr\005a0c65-1b4c-4e76-acdc-3752d5a06f1b.jsonl'

tool_calls = []       # (time, name, input_preview, result_preview)
assistant_msgs = 0
user_msgs = 0

with io.open(p, 'r', encoding='utf-8') as f:
    for line in f:
        try:
            obj = json.loads(line)
        except Exception:
            continue

        t = obj.get('type', '')
        ts = obj.get('timestamp', '?')

        # 工具调用（assistant 消息里的 tool_use）
        msg = obj.get('message', {})
        content = msg.get('content', '')
        if isinstance(content, list):
            for c in content:
                if isinstance(c, dict) and c.get('type') == 'tool_use':
                    name = c.get('name', '?')
                    inp = json.dumps(c.get('input', {}), ensure_ascii=False)[:300]
                    tool_calls.append((ts, name, inp, None))
                if isinstance(c, dict) and c.get('type') == 'tool_result':
                    if tool_calls:
                        tid = c.get('tool_use_id')
                        res = json.dumps(c.get('content', ''), ensure_ascii=False)[:200]
                        # match by id
                        for i in range(len(tool_calls)-1, -1, -1):
                            pass
                        tool_calls[-1] = (tool_calls[-1][0], tool_calls[-1][1], tool_calls[-1][2], res)

        if t == 'user':
            user_msgs += 1
        if t == 'assistant':
            assistant_msgs += 1

print(f'assistant msgs: {assistant_msgs}, user msgs: {user_msgs}')
print(f'tool calls found: {len(tool_calls)}')
for ts, name, inp, res in tool_calls:
    print(f'\n--- {ts} ---')
    print(f'  tool: {name}')
    print(f'  input: {inp}')
    if res:
        print(f'  result: {res}')
