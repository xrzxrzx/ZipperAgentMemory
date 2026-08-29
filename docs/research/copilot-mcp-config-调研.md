# GitHub Copilot + MCP：VS Code 与 Visual Studio 官方配置格式调研（2026-08 快照）

> 调研日期：2026-08-21。所有内容均来自官方文档原文（微软 VS Code 文档、Microsoft Learn、GitHub Docs、GitHub Changelog、VS Code 源码 schema），仅报告官方文档所陈述的内容，不做猜测。所有引文为原文摘录（英文），未翻译处保留原文以便核对。
> 原始抓取文件保存在 `.research/mcp-docs/`（HTML 原文与提取文本）。

---

## A) VS Code（GitHub Copilot Chat）

### A.1 官方文档

| 页面 | URL | 说明 |
|---|---|---|
| MCP configuration reference（现行） | https://code.visualstudio.com/docs/agents/reference/mcp-configuration | 配置 schema / 命令 / 设置；页面底部显示 8/19/2026 更新 |
| Add and manage MCP servers（现行） | https://code.visualstudio.com/docs/agent-customization/mcp-servers | 添加/管理 MCP 服务器、UI 路径、信任、故障排查 |
| 旧版（2025 年 4 月起） | 原路径 `docs/copilot/chat/mcp-servers.md`（标题 "Use MCP servers in VS Code (Preview)"），2026 年 2 月重构成 `docs/copilot/reference/mcp-configuration.md`，2026 年 6 月再迁移到 `docs/agents/...` | 历史版本（git 历史确认：commit 9673b63c8a 2025-04-28；restructure 2026-02-22、2026-06-02） |

### A.2 工作区级配置文件：`.vscode/mcp.json`

官方原文（MCP configuration reference）：

> "MCP server configuration is stored in the `mcp.json` JSON file. This file can be in your workspace (`.vscode/mcp.json`) or in your user profile."

**顶层键**（现行 schema，来自官方文档 + VS Code 源码 `src/vs/workbench/contrib/mcp/common/mcpConfiguration.ts` 的 `mcpServerSchema`）：

- `"servers": {}` — 必填，对象：服务器名 → 配置对象（"Each key is the server name, and the value is the server configuration object"）
- `"inputs": []` — 可选，敏感信息输入变量定义
- `"sandbox": {}` — 可选，仅 macOS/Linux，沙箱文件系统/网络规则

**per-server 键**（现行官方字段表）：

stdio（type `"stdio"`）：

| 字段 | 必填 | 说明 |
|---|---|---|
| `type` | 是 | 取值 `"stdio"` |
| `command` | 是 | 启动命令，需在 PATH 或使用完整路径（如 `"npx"`、`"node"`、`"python"`、`"docker"`） |
| `args` | 否 | 参数数组，如 `["server.py", "--port", "3000"]` |
| `cwd` | 否 | 工作目录，默认工作区文件夹 |
| `env` | 否 | 环境变量，值可为 string/number/null |
| `envFile` | 否 | 环境文件路径 |
| `dev` | 否 | 开发模式（watch/debug） |
| `sandboxEnabled` | 否 | 仅 macOS/Linux |

HTTP / SSE（type `"http"` 或 `"sse"`）：

| 字段 | 必填 | 说明 |
|---|---|---|
| `type` | 是 | 取值 `"http"`、`"sse"`（源码 schema 枚举：`enum: ['http', 'sse']`） |
| `url` | 是 | 服务器 URL，须以 `http://` 或 `https://` 开头 |
| `headers` | 否 | 认证/配置用的 HTTP 头 |
| `oauth` | 否 | OAuth 配置（`clientId` 等） |

**关于 `enabled` 与 `timeout`：现行官方 schema 与文档中均不存在这两个键**（VS Code 源码 `mcpStdioServerSchema` / HTTP schema 里没有 `enabled`、没有 `timeout`）。工作区级服务器**不需要** `"enabled": true`。官方现在的做法是：首次启动时弹**信任对话框**（trust dialog）；启用/禁用通过 UI 管理，且官方明确说明：

> "The enable/disable state is stored separately from the server configuration in `mcp.json`, so it does not affect shared configuration files."

（注意：网上不少第三方教程里出现的 `"enabled": true` / `"timeout"` 并非 VS Code 官方文档字段。）

**官方 JSON 示例**（MCP configuration reference）：

```json
{
  "servers": {
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"]
    }
  }
}
```

远程 HTTP 示例（官方）：

```json
{
  "servers": {
    "context7": {
      "type": "http",
      "url": "https://mcp.context7.com/mcp"
    }
  }
}
```

带输入变量（API key）示例（官方）：

```json
{
  "inputs": [
    {
      "type": "promptString",
      "id": "perplexity-key",
      "description": "Perplexity API Key",
      "password": true
    }
  ],
  "servers": {
    "perplexity": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "server-perplexity-ask"],
      "env": {
        "PERPLEXITY_API_KEY": "${input:perplexity-key}"
      }
    }
  }
}
```

**新增注意事项（现行文档）**：VS Code 会把配置转发给 "Agent Host"；但 "The Agent Host doesn't read `.vscode/mcp.json` directly; for portable configuration, use a workspace `.mcp.json` or user `~/.copilot/mcp-config.json` file, which the Agent Host reads natively."（需要交互输入如 `${input:...}` 的服务器不会被转发。）

### A.3 用户级配置文件（user-level mcp.json）

现行官方文档只说位置在“用户配置文件目录”，通过命令打开：

> "**User profile**: run the **MCP: Open User Configuration** command to open the `mcp.json` file in your user profile folder. Servers configured here are available across all your workspaces."

官方文档**没有**逐字写出绝对路径。依据 VS Code 官方 settings 文档（https://code.visualstudio.com/docs/getstarted/settings）：

> "Windows `%APPDATA%\Code\User\settings.json`"（macOS `$HOME/Library/Application Support/Code/User/settings.json`；Linux `$HOME/.config/Code/User/settings.json`）

用户级 `mcp.json` 与 `settings.json` 同处“用户配置文件目录”，因此 Windows 上为 `%APPDATA%\Code\User\mcp.json`（这是基于官方两份文档的组合推断，官方 MCP 文档未逐字给出该路径）。

历史（2025 年 4 月旧版文档）：用户级配置当时是写在用户 `settings.json` 的 `"mcp"` 段里：

```json
// settings.json
{
  "mcp": {
    "servers": {
      "my-mcp-server": {
        "type": "stdio",
        "command": "my-command",
        "args": []
      }
    }
  }
}
```

### A.4 版本与设置项

- **引入版本：VS Code 1.99（2025 年 3 月发布）**。官方（1.99 发布说明 + 2025 年 4 月原文档 + GitHub Docs 前置条件）：
  - 1.99 发布说明："This release supports Model Context Protocol (MCP) servers in agent mode."
  - 原文档："MCP support is available starting in VS Code release 1.99."
  - GitHub Docs 前置条件："Visual Studio Code version 1.99 or later."
- **设置项**：官方文档中出现的是 **`chat.mcp.enabled`**（2025 年 4 月起，标记 Preview，默认 `true`，官方原文 "This setting is enabled by default"），**不是 `github.copilot.chat.mcp.enabled`**——`github.copilot.chat.mcp.enabled` 这一确切名称从未出现在官方文档中（该前缀用于其他 Copilot 设置，如 `github.copilot.chat.agent.autoFix`、`github.copilot.chat.agent.runTasks`）。
- **VS Code 1.104（2025 年 8 月）起更名**为 **`chat.mcp.access`**，官方发布说明原文：

> "The `chat.mcp.enabled` setting that previously controlled whether MCP servers could run in VS Code has been migrated to a new `chat.mcp.access` setting with more descriptive options:
> - `all`: allow all MCP servers to run (equivalent to the previous `true` value)
> - `none`: disable MCP support entirely (equivalent to the previous `false` value)"

- 现行 AI Settings Reference（https://code.visualstudio.com/docs/agents/reference/ai-settings）中与 MCP 相关的设置及默认值：
  - `chat.mcp.access` — "Manage which Model Context Protocol (MCP) servers can be used in VS Code."（表格默认值显示为 `true`）
  - `chat.mcp.discovery.enabled` — 自动发现其他应用的 MCP 配置，默认 `false`（1.104 起默认关闭）
  - `chat.mcp.serverSampling` — 默认 `{}`
  - `chat.mcp.apps.enabled`（Experimental）— 默认 `true`
  - `chat.mcp.autostart`（Experimental）— 默认 `newAndOutdated`

### A.5 从 UI 添加 / 管理 MCP 服务器（官方路径）

- **扩展视图画廊**：Extensions 视图搜索 `@mcp` → 选服务器 → `Install`（装到用户配置）或右键 `Install in Workspace`（写入 `.vscode/mcp.json`）。官方："This updates the `.vscode/mcp.json` file in your workspace."
- **命令面板**：`MCP: Add Server`（引导式，选 Workspace 或 Global）、`MCP: List Servers`（start/stop/restart/show output）、`MCP: Reset Cached Tools`、`MCP: Reset Trust`、`MCP: Open User Configuration` / `MCP: Open Workspace Folder MCP Configuration`。
- **扩展视图 MCP SERVERS - INSTALLED 段**：右键或齿轮图标，可 Enable/Disable（"Right-click a server in the MCP SERVERS - INSTALLED section of the Extensions view and select Enable or Disable"）。
- **mcp.json 编辑器内联动作（code lens）**：保存后有 "Start" 按钮；GitHub Docs："A 'Start' button will appear in your `.vscode/mcp.json` file, at the top of the list of servers."
- **聊天内工具图标**（GitHub Docs）："click the tools icon in the top left corner of the chat box. This will open the MCP server list."
- **CLI**：`code --add-mcp '{ "name": "my-server", "command": "uvx", "args": ["mcp-server-fetch"] }'`

### A.6 Copilot 订阅/套餐要求

官方（GitHub Docs "Extending GitHub Copilot Chat with Model Context Protocol (MCP) servers"）前置条件：

> "**Prerequisites**: Access to Copilot. · Visual Studio Code version 1.99 or later. · If you are a member of an organization or enterprise with a Copilot Business or Copilot Enterprise plan, the 'MCP servers in Copilot' policy must be enabled in order to use MCP with Copilot."

即：文档层面未要求“付费套餐”，只要求“有 Copilot 访问权限”。企业策略说明：

> "The MCP policy only applies to users who have a Copilot Business or Copilot Enterprise subscription from an organization or enterprise that configures the policy. Copilot Free, Copilot Pro, Copilot Pro+, or Copilot Max do not have their MCP access governed by this policy. ... The policy is disabled by default."

GitHub 官方博客（2025-04-04，"Vibe coding with GitHub Copilot: Agent mode and MCP support rolling out to all VS Code users"）：

> "We are excited to roll out agent mode in Visual Studio Code to all users, now complete with MCP support..."

即 agent mode + MCP 面向**所有** VS Code 用户（含 Free 计划）推出；高级模型走付费计划的 premium requests。Visual Studio 的 2025 版文档也曾明确 "You can use GitHub Copilot for Free."

### A.7 传输命名："http" vs "streamable-http"

- **现行 VS Code 配置值就是 `"http"`**，官方字段表："`type`: Server connection type — `"http"`, `"sse"`"。
- "streamable-http" **从来不是** VS Code 的合法 `type` 取值（源码 schema 枚举只有 stdio/http/sse）；它是 MCP 协议层面传输的名称（Streamable HTTP，前身 HTTP+SSE）。
- 官方现行行为（MCP configuration reference）：

> "Use this configuration for servers that communicate over HTTP. VS Code first tries the HTTP Stream transport and falls back to SSE if HTTP is not supported."

- VS Code 1.100（2025 年 4 月）发布说明："MCP support for Streamable HTTP ... Streamable HTTP servers are configured just like existing SSE servers, and our implementation is backwards-compatible with SSE servers"，示例为 `{"servers": {"my-mcp-server": {"url": "http://localhost:3000/mcp"}}}`。
- 结论：URL 型服务器用 `"type": "http"`（Streamable HTTP，自动回退 SSE）；SSE 仍可用 `"type": "sse"`，未废弃；`"streamable-http"` 不是 VS Code 字段。

---

## B) Visual Studio（Windows，VS 2022）

### B.1 官方文档

- 英文：https://learn.microsoft.com/en-us/visualstudio/ide/mcp-servers?view=vs-2022 （"Use MCP servers in Visual Studio"，页面显示 Last updated 2026-07-30）
- 中文：https://learn.microsoft.com/zh-cn/visualstudio/ide/mcp-servers?view=vs-2022 （内容与英文一致：前置条件同样为“Visual Studio 2026 或 Visual Studio 2022 版本 17.14”）
- 旧版（2025 年 5 月）：`docs/ide/mcp-servers.md`，标题 "Use MCP servers in Visual Studio (Preview)"

### B.2 引入版本：17.14（不是 17.13）

- 现行文档前置条件（英/中一致）："Visual Studio 2026 **Or** Visual Studio 2022 version 17.14 (with the latest servicing release recommended for the most up-to-date MCP features)"。
- GitHub Changelog（2025-06-17，VS 17.14 June release）："Connect Visual Studio to your stack with MCP server support (Preview)" 正文："Visual Studio now supports Model Context Protocol (MCP) servers (Preview)... To get started, add an `mcp.json` file to your solution. Visual Studio will detect it automatically, including configurations from environments like `.vscode/mcp.json`. You can view your connected MCP servers in the Copilot Chat **Tools** dropdown. Realize that you must be in agent mode to use MCP integrations."
- 2025 年 5 月版文档前置条件："Visual Studio 2022 version 17.14 or later" + "Set **Enable agent mode in the chat pane** in **Tools > Options > GitHub > Copilot**."

### B.3 配置方式与 “Add MCP server” 对话框

**从聊天添加**（现行 VS 文档）：

> "In the chat pane, switch to Agent mode... Select **Tools** to open the tool picker. In the tool picker, select the plus (+) button. On the menu that appears, select **Add custom MCP server**. The **Add MCP server dialog** opens. In the Add MCP server dialog, enter the server name and connection details, such as the URL for HTTP servers or the command and arguments for stdio servers."

**对话框字段**（GitHub Docs "Extending GitHub Copilot Chat with MCP servers" 的 Visual Studio 部分）：

> "In the 'Configure MCP server' pop-up window, fill out the fields, including server ID, type, and any additional fields required for the specific MCP server configuration."

- 远程服务器示例：`Server ID: github`，`Type: "HTTP/SSE"`，`URL: https://api.githubcopilot.com/mcp/`
- 本地服务器示例：`Server ID: github`，`Type: "stdio"`，`Command (with optional arguments): docker "run", "-i", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "ghcr.io/github/github-mcp-server"`，加环境变量 `GITHUB_PERSONAL_ACCESS_TOKEN`

**是否存在 Tools → Options → GitHub → Copilot → MCP 设置页？** 现行文档中**没有**专门的 MCP 设置页。`Tools > Options > GitHub > Copilot` 下的相关项是：
- 旧版文档："Enable agent mode in the chat pane"（启用 agent mode 勾选项）；
- 现行文档："Show trust dialog before running tools from an updated MCP server"（"If you don't see this setting, update to Visual Studio 2026 version 18.7 or later"）；
- `Tools > Options > GitHub > Copilot > Tools`：重置工具确认（Allow/Confirm）选择。
另外 `Extensions > MCP Registries...` 打开 "MCP Server Manager" 安装注册表服务器。

### B.4 mcp.json 文件（VS 也读 .vscode/mcp.json）

现行文档：

> "Visual Studio supports the use of `mcp.json` files to store configuration information for MCP servers. `mcp.json` files store each server's name, transport type (for example, stdio or SSE), the command to launch it, any arguments, and optional environment variables needed to connect to that server."

**自动发现顺序（官方原文列表，按此顺序读取）**：

1. `%USERPROFILE%\.mcp.json` — "Serves as a global MCP server configuration for a specific user. Adding an MCP server here makes it load for all Visual Studio solutions."
2. `<SOLUTIONDIR>\.vs\mcp.json` — "Specific to Visual Studio and loads the specified MCP servers only for a specific user, for the specified solution."
3. `<SOLUTIONDIR>\.mcp.json` — "Works well if you're looking for an MCP configuration that you can track in source control for a repository."
4. `<SOLUTIONDIR>\.vscode\mcp.json` — "Scoped to the repository/solution and typically not source controlled."
5. `<SOLUTIONDIR>\.cursor\mcp.json` — "Scoped to the repository/solution and typically not source controlled."
   - "Some of these locations require `.mcp.json`, whereas others require `mcp.json`."

即：**是的，VS 会读取解决方案目录下的 `.vscode\mcp.json`（以及 `.cursor\mcp.json`）**——官方明说 "Visual Studio also checks for MCP configurations that other development environments set up." 配置格式为标准的 `{"servers": {...}}`（官方示例）：

```json
{
  "servers": {
    "github": {
      "url": "https://api.githubcopilot.com/mcp/"
    }
  }
}
```

本地 stdio 示例（官方 2025 版文档）：

```json
{
  "inputs": [
    {
      "id": "github_pat",
      "description": "GitHub personal access token",
      "type": "promptString",
      "password": true
    }
  ],
  "servers": {
    "github": {
      "type": "stdio",
      "command": "docker",
      "args": ["run", "-i", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "ghcr.io/github/github-mcp-server"],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "${input:github_pat}"
      }
    }
  }
}
```

官方对格式的概括（措辞较松散）："The format must follow the MCP specification. For example, it must include an array of server objects, each with `name`, `command` or `url`, and `transport`."（实际示例与 VS Code 相同，为 `servers` 对象映射。）

**与 VS Code 的同步**：文档没有描述“实时同步机制”，而是因为双方都从相同路径读取（`<SOLUTIONDIR>\.vscode\mcp.json` 等共享位置），同一文件天然被两个编辑器复用。官方还提到：把 mcp.json 加入版本控制时，把文件位置加到 Solution Explorer 的 Solution Items；"When you save the file with valid syntax, the GitHub Copilot agent restarts and reloads the configured servers."

### B.5 MCP 工具出现在哪里 / 启停

- 必须处于 **Agent mode**（现行文档与 Changelog 均强调；工具在聊天底部模式下拉切到 Agent）。
- 工具在 Copilot Chat 的 **Tools** 下拉里查看/启用；**默认禁用，需手动启用**："The tools are disabled by default and you must manually enable them."
- 调用工具时 Copilot 请求确认，用 Allow/Confirm 下拉选择“本次会话/本解决方案/以后全部”。
- 远程服务器 OAuth：在 `.mcp.json` 的 CodeLens 上点 "Auth" 完成认证（GitHub Docs：点 `Auth` 进行账号认证）。
- 管理端控制：GitHub Copilot dashboard 的策略（"Editor preview features flag" 旧称 / "MCP servers in Copilot" 策略）+ MCP server allow list 策略。

### B.6 传输支持（VS）

2025 年 5 月版官方文档原文：

> "Visual Studio supports local standard input/output (`stdio`), server-sent events (`sse`), and streamable HTTP (`http`) for MCP server transport."

现行文档：传输类型示例为 "stdio or SSE"；GitHub Docs 的 UI 下拉为 "HTTP/SSE"。即 VS 支持 stdio 与 HTTP（Streamable HTTP，配置值 "http"），SSE 也支持。

---

## C) 传输支持对照表

| 传输 | VS Code 配置值 | VS 2022 配置值 | 说明 |
|---|---|---|---|
| stdio（command + args） | `"type": "stdio"` + `command`/`args` | `"type": "stdio"` + `command`/`args` | 两者均支持，本地服务器最常用 |
| Streamable HTTP（URL，如 `http://127.0.0.1:8931/mcp`） | `"type": "http"` + `url` | `"type": "http"`（GitHub Docs UI 标为 "HTTP/SSE"）+ `url` | 配置值统一是 `"http"`；“streamable-http”不是任何编辑器的合法 type 值 |
| SSE | `"type": "sse"` + `url` | 支持（sse） | VS Code 对 `"http"` 先尝试 HTTP Stream，不支持时回退 SSE（官方原文见 A.7） |

要点：
- 官方文档中 **不存在 `"streamable-http"` 这个 type 值**；“Streamable HTTP” 是 MCP 协议传输名（未废弃，是现行 HTTP 传输），配置键用 `"http"`。
- 两个编辑器都支持 stdio 与 HTTP；`http://127.0.0.1:8931/mcp` 这类本地 URL 直接用 `"type": "http", "url": "http://127.0.0.1:8931/mcp"`（VS Code schema 要求 URL 以 http:// 或 https:// 开头）。

---

## D) 坑点汇总（Gotchas，均出自官方文档）

**VS Code**
1. 工作区级服务器不需要 `"enabled": true`（该键不在官方 schema）；首次启动有**信任对话框**，不信任则不启动（"If you don't trust the MCP server, it will not be started"）。从 mcp.json 直接启动服务器**不会**再弹信任提示（"If you start the MCP server directly from the mcp.json file, you will not be prompted to trust"）。
2. 启用/禁用状态**单独存储**，不写进 mcp.json。
3. 服务器按需启动；配置变更后需要重启/启动服务器（mcp.json 里的 Start 按钮或 `MCP: List Servers`）；工具变更后可用 `MCP: Reset Cached Tools`。
4. 沙箱（`sandboxEnabled`）仅 macOS/Linux，Windows 不可用。
5. 用户配置目录里的服务器在本机运行；远程机器上运行需用远程用户配置（`MCP: Open Remote User Configuration`）。
6. Agent Host（2026 新机制）不直接读 `.vscode/mcp.json`，跨端可移植配置用工作区 `.mcp.json` 或用户 `~/.copilot/mcp-config.json`。
7. Docker 启动 stdio 服务器不要用 `-d`（detach）。
8. 设置同步（Settings Sync）需勾选 "MCP Servers" 选项才会同步 MCP 配置。
9. `chat.mcp.discovery.enabled` 自 1.104 起默认关闭。

**Visual Studio**
1. 工具**默认禁用**，必须手动启用；必须处于 Agent mode。
2. 文件命名易混：有的位置是 `.mcp.json`，有的是 `mcp.json`（`.vs\mcp.json`、`.vscode\mcp.json`、`.cursor\mcp.json` 用 `mcp.json`；`%USERPROFILE%\.mcp.json`、`<SOLUTIONDIR>\.mcp.json` 用 `.mcp.json`）。
3. mcp.json 保存为合法 JSON 后 Copilot 代理自动重启并重载服务器。
4. 组织管理员可通过 GitHub Copilot dashboard 策略（"MCP servers in Copilot"，默认禁用，仅约束 Business/Enterprise）和 MCP allow list 控制 VS/VS Code 中的 MCP 使用。
5. 17.14 最新服务更新才含最新 MCP 功能（含从 Web 直接安装）；信任对话框等功能在 VS 2026 18.7+。

---

## 参考链接（官方）

- VS Code MCP configuration reference：https://code.visualstudio.com/docs/agents/reference/mcp-configuration
- VS Code Add and manage MCP servers：https://code.visualstudio.com/docs/agent-customization/mcp-servers
- VS Code AI Settings Reference：https://code.visualstudio.com/docs/agents/reference/ai-settings
- VS Code settings（用户设置位置）：https://code.visualstudio.com/docs/getstarted/settings
- VS Code 1.99 / 1.100 / 1.104 发布说明：https://code.visualstudio.com/updates/v1_99 、v1_100、v1_104
- VS Code 源码 mcp.json schema：`microsoft/vscode` → `src/vs/workbench/contrib/mcp/common/mcpConfiguration.ts`（`mcpStdioServerSchema` / `mcpServerSchema`）
- GitHub Docs — Extending GitHub Copilot Chat with MCP servers：https://docs.github.com/en/copilot/how-tos/provide-context/use-mcp-in-your-ide/extend-copilot-chat-with-mcp
- GitHub Docs — About Model Context Protocol (MCP)：https://docs.github.com/en/copilot/concepts/context/mcp
- GitHub Docs — Enhancing GitHub Copilot agent mode with MCP：https://docs.github.com/en/copilot/tutorials/enhance-agent-mode-with-mcp
- GitHub Blog — Agent mode and MCP support rolling out to all VS Code users（2025-04-04）：https://github.blog/news-insights/product-news/github-copilot-agent-mode-activated/
- GitHub Changelog — Visual Studio 17.14 June release / MCP (Preview)（2025-06-17）：https://github.blog/changelog/2025-06-17-visual-studio-17-14-june-release/
- Microsoft Learn — Use MCP servers in Visual Studio（EN）：https://learn.microsoft.com/en-us/visualstudio/ide/mcp-servers?view=vs-2022 （中文：https://learn.microsoft.com/zh-cn/visualstudio/ide/mcp-servers?view=vs-2022 ）
