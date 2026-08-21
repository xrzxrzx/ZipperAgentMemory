# Go 编码规范（ZipperAgentMemory）

> 状态：生效（随项目初始化）　适用范围：本仓库全部 Go 代码
> 依据：Effective Go、Go Code Review Comments、本仓库 AGENTS.d 系列约定

## 1. 总则

1. 代码以 `gofmt` / `goimports` 输出为准，禁止手调格式；
2. 每个包必须有 `doc.go` 或包级注释说明职责；
3. 提交前必须 `go vet ./...` 与 `go build ./...` 零告警通过；
4. 本工具运行于低资源服务器，**资源意识是硬要求**：无泄漏的 goroutine、有界的缓冲、按需加载。

## 2. 命名

| 对象 | 规则 | 示例 |
|------|------|------|
| 包名 | 小写单词，无下划线，尽量短 | `memory`, `index`, `mcp` |
| 文件 | 蛇形，与主标识符对应 | `memory_store.go` |
| 导出标识符 | 首字母大写，完整单词 | `OpenStore` |
| 未导出标识符 | 驼峰小写 | `openStore` |
| 接口 | 行为命名，`-er` 后缀 | `Storer`, `Searcher` |
| 常量/错误 | `Err` 前缀 + 描述 | `ErrPathOutsideRoot` |
| 测试 | `_test.go` 后缀，表驱动 | `TestMemoryRead` |

禁止：中文标识符、拼音命名、`a1/b2` 式缩写（循环变量 `i/j/k` 除外）。

## 3. 错误处理

1. 错误必须被检查；`_` 丢弃 error 仅限明确无副作用且注释说明的场景；
2. 错误用 `fmt.Errorf("上下文: %w", err)` 包装，保留链条；顶层统一 `%+v` 或 MCP JSON-RPC error 输出；
3. 定义哨兵错误（`var ErrXxx = errors.New(...)`）供 `errors.Is` 判断，禁止字符串比较；
4. **禁止 panic**（除 `main` 中不可恢复的启动错误）；库代码一律返回 error；
5. 调用方只需关心是否成功的场景，用 `if err != nil { return ... }` 快速路径。

## 4. 并发

1. 共享可变状态一律显式加锁（`sync.Mutex`/`sync.RWMutex`），**禁止裸全局变量跨 goroutine 读写**；
2. 写操作串行化：进程内互斥锁 + 「临时文件 + rename」原子写（见 §6）；
3. goroutine 必须有明确的退出路径：配合 `context.Context` + 关闭的 channel，禁止后台泄漏；
4. `fsnotify` 事件必须去抖合并（time.Ticker 窗口），禁止逐事件同步做 IO；
5. 能不用 goroutine 就不用：低资源服务器上按需同步执行优先。

## 5. 文件与路径安全

1. 所有用户/agent 提供的路径必须经沙箱校验：`filepath.Clean` 后判断是否落在根目录内，**禁止符号链接逃逸**；
2. 符号链接解析**必须用组件级 `Lstat + os.Readlink`**（加递归深度上限防链接环），**禁止只用 `filepath.EvalSymlinks`**——Windows 上 EvalSymlinks 不解析目录联接（junction），存在写穿根目录漏洞（阶段 1 实测发现并修复，见 docs/验收/阶段1.md §6.1）；组件级解析对符号链接与 junction 均有效，Linux 语义与 EvalSymlinks 一致；
3. 路径穿越一律返回 `ErrPathOutsideRoot`，不静默裁剪；
4. 禁止直接用用户输入拼 `exec.Command` 参数；调 git 等外部命令时必须参数数组传参（禁用 shell 字符串）；
5. 文件读写用 `os.OpenFile` + 显式权限（0644/0755），不依赖 umask 猜测。

## 6. 原子写与索引一致性

1. 写文件流程：写 `临时文件（同目录）` → `fsync` → `rename` 覆盖目标 → 更新 SQLite 索引；
2. 索引是缓存不是真源：任何不一致可 `rebuild-index` 子命令恢复，代码不得假设索引必然最新；
3. 删除操作：先删文件，后删索引记录；顺序不可颠倒。

## 7. 依赖与模块

1. 依赖最小化：能用标准库绝不加第三方包；新增依赖必须写入设计文档并经用户批准；
2. `go.mod` 锁定 Go 版本；依赖升级走独立提交，注明理由；
3. 禁止 fork 第三方库（除非记录在案并说明理由）。

## 8. 测试

1. 关键逻辑（路径沙箱、原子写、索引、MCP 参数校验）必须有单元测试，表驱动优先；
2. 测试不得依赖网络；临时文件用 `t.TempDir()`；
3. 提交前 `go test ./...` 全绿；集成测试（MCP 握手、迁移脚本）放 `scripts/` 或 `integration/`，CI 之外手动可跑。

## 9. Git 提交（引用本仓库 AGENTS.d/git.md）

- 提交信息：Conventional Commits（`feat:` `fix:` `docs:` `refactor:` `test:` `chore:`），正文说明动机；
- 禁止 `git rebase` / `force-push`；合并一律 Squash Merge；
- 「不主动提交」场景：小改动、临时需求、无法确保用户满意时，先询问用户再提交。

## 10. 资源预算红线（本工具特有）

| 红线 | 值 |
|------|----|
| 常驻内存 | < 60MB（阶段 2 起实测，`/usr/bin/time -v` 记录在案） |
| 空闲 CPU | ≈0（无轮询，纯事件驱动） |
| 禁止引入 | 向量库、embedding 模型、ES、watchman 等重量级组件 |

违反红线的改动需在设计文档补充说明并重新获批。
