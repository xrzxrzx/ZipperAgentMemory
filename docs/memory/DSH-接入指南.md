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

## 查看 Web 阅读页口令

浏览器只读入口：`https://zipper.net.cn/memory/`（设计见 `docs/design.md §11`）。
口令存在服务器文件里，**只在服务器侧读取，绝不写进记忆库、仓库或聊天记录**：

```powershell
# 本机 PowerShell：一行取到剪贴板（屏幕不回显完整口令）
(ssh zipper-server "cat /opt/zipper-agent-memory/web-token").Trim() | Set-Clipboard
# 只想确认取对了（显示前 4 位 + 长度，完整值仍进剪贴板）
$t = (ssh zipper-server "cat /opt/zipper-agent-memory/web-token").Trim(); $t | Set-Clipboard; "{0}…（{1} 字符）" -f $t.Substring(0,4), $t.Length
```

> 上例用本机 SSH 别名 `zipper-server`（旧文档写的 `minichat-server` 是同一台 `8.141.89.50`，按你 ssh config 里的实际别名替换即可）。

- 口令**长期有效**，只需在**首次登录、换设备或清空浏览器 Cookie** 后重新输入；
- 未授权访问一律 401（接口）或跳登录页（页面导航）；连续失败 10 次/分钟会被限流；
- 轮换口令与泄露处置见 `docs/部署手册.md §4`「阅读页口令」。

## 客户端接入（Claude/Codex）

**主方案：SSH 隧道（2026-09-29 起，已配成常驻自启）**

- DSH 侧落点是 **profile patch**：在 `~/.dsh/profiles/desktop/cordis.patch.yml` 里追加一个 **`- insert:` 块**
  （顶层 `- id: <新 id>` 是「定向覆盖**已存在**条目」，用它新增会报 `patch: entry "..." not found` —— 2026-09-29 重装实测踩过）：

  ```yaml
  - insert:
      - id: mcp-zipper-memory
        name: '@deepseek-ai/dsh-mcp-client'
        config:
          serverName: zipper-memory          # 决定工具名 mcp__zipper-memory__*
          transport: streamable-http
          url: http://127.0.0.1:8931/mcp
  ```

  写入必须用**临时文件 + 原子移动**（`Move-Item -Force`）才会被 watcher 认作一次 change；改完 `logs/host/dsh-<日期>.log` 应无 `YAMLException`、无 `entry ... not found`。
  > ⚠️ **旧说法已作废**：`~/.dsh/dsh-mcp-client-v2.json` 在本版 DSH（0.1.7-rc.2）里**并不存在**——全安装目录 grep `dsh-mcp-client-v2.json` 零命中，`dsh-mcp-client` 插件只认 cordis 配置（2026-09-29 新机重装实测）。
- 隧道监督者 `~/.dsh/zipper-memory-tunnel.ps1`（断线 5s 自动重连）+ 启动文件夹 vbs 登录自启
  - ⚠️ 手工拉起必须用 `explorer.exe "<vbs 路径>"` 代启；**别在 DSH 会话里直接跑**，否则 DSH 重启会把隧道连带杀掉
- 服务端只看到 `127.0.0.1`（白名单内）→ **公网 IP 漂移免疫**；详见 `decisions/2026-09-29-MCP隧道根治.md`

**应急方案：公网直连**（仅当本机出口 IP 与白名单一致时可用）

- `http://8.141.89.50:8931/mcp`
  - ⚠️ 白名单是**精确 IP 匹配**（不支持网段），家宽 IP 漂移即 403。排查见 `decisions/2026-09-20-公网IP漂移-MCP-403.md`

**其他设备 / 外出**：`ssh -N -L 8931:127.0.0.1:8931 minichat-server` 后连 `http://127.0.0.1:8931/mcp`
- MCP 工具：`memory_read/write/append/search/list/status`（6 个，带行为标注）
