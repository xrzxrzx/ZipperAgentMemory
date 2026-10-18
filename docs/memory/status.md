# 项目状态快照

> 更新规则：每次阶段结束/重要决策后更新本文件，保持为「当前事实」，不保留历史（历史见 git）。

- 更新日期：**2026-10-18**
- 当前阶段：**v0.4.0 已交付并在生产运行**；2026-10-05 追加「记忆阅读页」（`需求-001`，已上线）；2026-10-14 完成「根外目录预言机修复」（`需求-004`，本地已提交 `e4a031a` / `3b1df3f`，**未部署**）；2026-10-18 完成「检索质量优化」（`需求-005`，本地 `44cbb3a` + `c54e3c2`，**未部署**；**索引内容格式已变更**，部署后须 `rebuild-index`，`serve` 启动自动重建）

## 生产运行态（2026-10-05 实测）

| 项 | 值 |
|---|---|
| 服务器 | `8.141.89.50`（hostname `iZ2zecnoagydl6f1ai6o32Z`；本机 SSH 别名 **`zipper-server`**，旧文档写的 `minichat-server` 是同一台） |
| 进程托管 | systemd `zipper-agent-memory.service`（`ExecStart = … serve -root /opt/zipper-agent-memory/memory -addr 0.0.0.0:8931 -config /opt/zipper-agent-memory/config.json`） |
| MCP | `http://0.0.0.0:8931/mcp`，IP 白名单 2 条（`127.0.0.1` + 家庭 IP）；本机经 SSH 隧道接 `127.0.0.1:8931` |
| 阅读页 | `http://127.0.0.1:8932/memory/`，由 nginx 反代为 **`https://zipper.net.cn/memory/`**（443，复用既有证书；主域其余路径 404） |
| 阅读页口令 | `/opt/zipper-agent-memory/web-token`（0600，43 字符）；查看/轮换见 `docs/部署手册.md §4` |
| 记忆库 | `/opt/zipper-agent-memory/memory`｜**271 文件 / 714,963 字节**｜索引 **41 文件**（`indexableExts` 限定 `.md/.csv/.txt/.json/.yaml/.yml/.toml`，`.go` 等不入索引） |
| 内存红线 | 实测 **RSS 19.1MB**（红线 60MB ✅） |

## 已确认决策（累积，权威见 `docs/design.md §10`）

| # | 决策 | 状态 |
|---|---|---|
| D1–D7 | 接入方通用 MCP / CSV+Markdown / 迁移双模式 / Linux / **Go** / 混合内容 / 项目名 ZipperAgentMemory | ✅ |
| 决策 3 | git autocommit **默认开启**，每日 0 点提交 | ✅ |
| 决策 6 | 8931 公网开放 + **IP 白名单**（精确匹配）；本机改走 SSH 隧道，与 IP 漂移解耦 | ✅ |
| 决策 8 | **阅读页**：路径式入口 `https://zipper.net.cn/memory/`、长期口令、**全库可读（风险知情接受）**、其余路径 404、只读无写接口 | ✅ 2026-10-05 上线 |
| 需求-004 | **阅读页文件系统访问收口 `memory.Store` 沙箱**（根外目录存在性预言机修复）：元数据查询下沉 `store`（`IsDir`/`ModTime`，先 `resolve` 再 `Stat`），web 层不再直接访问文件系统；「必经沙箱」写入编码规范 §5.6；详见 `decisions/2026-10-14-沙箱收口.md` | ✅ 2026-10-14 本地已提交（`e4a031a` / `3b1df3f`），**未部署** |

## 待办 / 开放项

| # | 项 | 归属 |
|---|---|---|
| 1 | **P2-4 决策**：反代下失败限流退化为全局单桶（10 次/分钟），任意可达者可让所有者 60 秒内无法新登录（已登录不受影响）。三选一：可信 XFF（仅回环时信任）/ 放宽阈值 / 维持现状 | 用户 |
| 2 | 阅读页是否隐藏 `.git/` 等点目录（当前"全库可读"包含仓库内部） | 用户 |
| 3 | 浏览器人工目视：中文排版、目录导航、AND/OR 切换、窄屏、关浏览器重开仍登录 | 用户 |
| 4 | 登记待修的 P3：限流表非硬上限、缺 CSP/X-Frame-Options、检索片段高亮错位、Windows `NUL` 设备名、Cookie 无 `Expires` | 后续需求 |
| 5 | `version` 常量仍为 `v0.4.0`（`cmd/…/main.go` 与 `pkg/mcp/server.go` 同步），建议下次改动时 bump 到 v0.5.0，便于运维识别在跑版本 | 后续改动 |
| 6 | **部署需求-004 修复到服务器**：线上阅读页当前仍为修复前版本（根外目录预言机在生产仍存在，需有效口令才可利用）。本机交叉编译 → 上传 → 重启 `zipper-agent-memory.service`，属 `zipper-ops`，**须用户明确批准** | 用户 |
| 7 | `pkg/mcp/server.go:74` `indexFile` 同类写法收口（裸 `Join(store.Root(), rel)` + `os.ReadFile` + `os.Stat`；调用点仅在写成功路径、当前不可触发；需求-004 独立验证 P3-1 登记） | 后续需求 |
| 8 | 需求-004 独立验证的未覆盖项：Linux 平台、nginx 反代层、MCP 第二轮活体复跑、并发/性能压测 | 可选补验 |
| 9 | **需求-005 独立验证**（审查者 ≠ 作者）：AC6 命中集、`ErrIndexStale` 边界、单字不回归，须另起独立上下文复核 | 后续 |
| 10 | **需求-005 部署**：索引内容格式已变（`indexSchemaVersion=2`），本机交叉编译 → 上传 → 重启服务（启动自动 rebuild）。属 `zipper-ops`，**须用户明确批准** | 用户 |

## 环境事实（踩过的坑，别重复踩）

- **`go build ./...` / `go test ./...` / `go mod tidy` 在本仓库不可用**：`memory/notes/vs-copilot-deepseek-research/` 下有抓取回来的第三方 `.go` 副本，Go 会试图扩 go.mod。一律用**路径限定**形式：`go build ./cmd/... ./pkg/...`、`go test -count=1 ./cmd/... ./pkg/...`（`go mod tidy` 亦不可用，go.mod 手工维护）。
- **服务器 Go 工具链卡死**（`timeout 8 /usr/local/go/bin/go version` → exit=124）：构建走**本机交叉编译 + 上传**（`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`）。
- 本机终端是 **Windows PowerShell 5.1**：无 `-SkipHttpErrorCheck`；`curl.exe` 传 JSON 体要用**请求体文件**（`-d @file`），否则引号被吃掉；含中文的文件必须显式 UTF-8 读。
- **文档体系**：特性文档在 `docs/features/需求-NNN-<标题>/`（映射见 `AGENTS.d/docs.md §1.1`），跨需求状态真源是 `docs/features/台账.md`。

## 已知运维事件（与项目代码无关）

- **2026-10-05 19:11 / 19:15 两次整机 OOM**：trigger memcg = `hrcore-dingtalk.service`（该服务重启计数已达 7，持续以 status=2 退出）；被 OOM-killer 波及的含 `zipper-agent-memoryd`、`frps`、`systemd-journald`；systemd 均在 5 秒内自动拉起，事后全部 `active`。记忆库与索引无损坏（服务重启即重建索引）。
