# -*- coding: utf-8 -*-
"""向 Codex config.toml 追加 mcp_servers.zipper-memory 段"""
import io
import tomllib

p = r'C:\Users\xrzxr\.codex\config.toml'

with io.open(p, 'r', encoding='utf-8') as f:
    content = f.read()

section = '[mcp_servers.zipper-memory]'
if section not in content:
    block = '\n[mcp_servers.zipper-memory]\ntype = "http"\nurl = "http://8.141.89.50:8931/mcp"\n'
    with io.open(p, 'w', encoding='utf-8') as f:
        f.write(content + block)
    print('appended mcp_servers.zipper-memory')
else:
    print('already present')

# 验证
with open(p, 'rb') as f:
    cfg = tomllib.load(f)
print('valid TOML. mcp_servers =', cfg.get('mcp_servers'))
