# ZipperAgentMemory 记忆库访问（DSH 接入）

> 用途：DSH（本工具）访问 ZipperAgentMemory 记忆库的标准方式。
> 服务器：8.141.89.50（SSH alias `minichat-server`，密钥认证）
> 记忆库：`/opt/zipper-agent-memory/memory`（git 仓库，每日 00:05 同步 Gitee 私密库 ZipperAgentMemory-memory）

## 访问方式

所有操作通过 SSH 执行服务器上的 CLI（无需 MCP 配置）：

```powershell
# 搜索（支持中文，FTS5）
ssh minichat-server 'export PATH=/usr/local/go/bin:$PATH; cd /opt/zipper-agent-memory; ./zipper-agent-memoryd search -root memory "关键词"'

# 读文件
ssh minichat-server 'cat /opt/zipper-agent-memory/memory/<相对路径>'

# 写/追加（用 memory_write 语义：先写临时再原子替换）
# 通过 MCP 或直接编辑文件后自动索引
```

## 目录结构（五区）

- `notes/` 通用笔记（每主题一个 .md，蛇形文件名）
- `projects/` 项目知识（每项目一子目录）
- `structured/` 结构化数据（CSV）：`password-vault.csv`（密码薄57条）、`servers.csv`（服务器/数据库密码）
- `agent/` Agent 自动沉淀（每 agent 一目录，按月归档）
- `meta/` 元数据

## 写入规范（重要）

1. **路径沙箱**：所有相对路径必须落在 memory/ 根内，禁止 `../`、绝对路径、符号链接逃逸
2. **agent 沉淀**：写入 `agent/<agent-id>/YYYY-MM.md`，用追加方式（自动时间戳分隔）
3. **敏感数据**：密码类信息放 `structured/`，格式与现有 CSV 一致（UTF-8）
4. **写后索引**：服务端 fsnotify 自动索引（≤2s 可见）；也可主动 `rebuild-index`

## 主动提交

```bash
ssh minichat-server 'cd /opt/zipper-agent-memory && ./zipper-agent-memoryd git-commit -root memory'
```
（每日 00:05 自动提交+推送 Gitee；紧急变更可手动执行）

## 客户端接入（Claude/Codex）

- 家里直连：`http://8.141.89.50:8931/mcp`（IP 白名单：127.0.0.1 + 120.228.126.4）
- 外出：`ssh -N -L 8931:127.0.0.1:8931 minichat-server` 后连 `http://127.0.0.1:8931/mcp`
- MCP 工具：`memory_read/write/append/search/list/status`（6 个，带行为标注）
