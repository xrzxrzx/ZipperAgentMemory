# 服务器环境评估（只读，未做任何安装）

> 日期：2026-08-21　评估人：协调者　状态：✅ 具备开工条件（除 Go/git 未装外）

## 结论

| 项 | 状态 | 备注 |
|----|------|------|
| 连接 | ✅ | SSH alias `minichat-server`，密钥认证 |
| 包管理器 | ✅ | yum / dnf（阿里云 Linux 8，RHEL 系） |
| 网络 | ✅ | proxy.golang.org HTTP 200（Go 依赖可拉取）；GitHub 连通性待部署时实测 |
| 目录权限 | ✅ | /usr/local、/opt 可写（root） |
| 工具 | ✅ | tar / wget / curl 已有；**zip / unzip 缺失**（迁移打包若用 zip 需补装，或用 tar） |
| Go（服务器） | ❌ | 未装；需求 **≥1.25**（官方 go-sdk v1.7.x 要求）。安装路径建议：`dnf install golang` 或官方 tarball → /usr/local/go |
| git（服务器） | ❌ | 未装；`dnf install git` 即可 |
| **Go（本地开发机）** | ⚠️ | **已装 1.24.5（windows/amd64）< 1.25 不满足 go-sdk 要求**；批准后开工前需升级本地 Go ≥1.25（DSH 子 Agent 在本地编译验收 `go build/test`） |

## 部署规划（阶段 4/5 执行，届时再装）

1. `dnf install -y git`（如需 zip 支持再加 `zip unzip`）；
2. 安装 Go ≥1.25（推荐官方 tarball，版本固定可复现）；
3. `git clone` 项目到 `/opt/zipper-agent-memory`；
4. `go build` 产出单二进制；systemd service 托管 `zipper-agent-memoryd serve`；
5. 记忆库目录规划：`/opt/zipper-agent-memory/memory`（git 仓库）。

## 风险

- GitHub 443 通道在服务器上需同样配置（22 端口可能同样被墙）；若服务器能直连 GitHub 则免配置。
