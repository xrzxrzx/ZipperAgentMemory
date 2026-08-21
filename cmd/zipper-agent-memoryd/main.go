// 命令 zipper-agent-memoryd 是 ZipperAgentMemory 的记忆守护进程入口。
//
// 子命令（阶段 2/3/4 + 2026-08-22 决策 3/6）：
//
//	serve          常驻运行：启动时全量重建索引，随后 fsnotify 监听 memory/
//	                （去抖合并）增量维护索引，并同时提供 MCP streamable HTTP
//	                服务（默认 http://127.0.0.1:8931/mcp）；git autocommit
//	                默认开启——独立 goroutine 每日 0 点定时 git add -A &&
//	                commit 一次（design 决策 3）；-allow-ips 可配置公网部署的
//	                IP 白名单（决策 6）；SIGINT/SIGTERM 优雅退出。
//	git-commit     立即执行一次 git 提交（用户主动触发入口，决策 3；
//	                无变更时输出跳过）。
//	stdio          MCP stdio 模式（按需拉起，供纯 stdio 客户端）：启动时
//	                全量重建索引后以 stdin/stdout 提供 MCP 服务，客户端断开
//	                即退出。
//	rebuild-index  全量重建索引后退出（索引是 derived state，可随时重建）。
//	git-init       初始化 memory/ 为 git 仓库并补全本地 user.name/email
//	                （幂等，不碰全局配置；供迁移 bundle 等场景使用）。
//	search QUERY   全文检索并输出 路径+命中片段（供人工/测试）。
//	version        输出版本号。
//
// 用法示例：
//
//	zipper-agent-memoryd serve -root memory
//	zipper-agent-memoryd serve -root memory -addr 0.0.0.0:8931 -allow-ips "1.2.3.4,5.6.7.8"
//	zipper-agent-memoryd serve -root memory -git-autocommit=false
//	zipper-agent-memoryd git-commit -root memory
//	zipper-agent-memoryd stdio -root memory
//	zipper-agent-memoryd rebuild-index -root memory
//	zipper-agent-memoryd git-init -root memory
//	zipper-agent-memoryd search -root memory "Go 语言"
//
// 索引数据库默认放在 memory/ 根目录的同级（不进入被监听的目录树，避免
// 索引写入自身触发监听事件形成反馈；也保持 memory/（git 仓库）干净），
// 可用 -db 覆盖。仅用标准库 flag，不引第三方 CLI 框架（任务书约束）。
// stdio 模式下协议走 stdout，运行日志一律输出到 stderr。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zipper-agent-memory/pkg/git"
	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/mcp"
	"zipper-agent-memory/pkg/memory"
	"zipper-agent-memory/pkg/watch"
)

// version 是守护进程版本号，对应 docs/design.md 的阶段交付规划。
const version = "v0.4.0"

// logger 统一向 stderr 输出运行日志（stdout 留给 search 等命令式输出，
// 且阶段 3 的 stdio 模式需要 stdout 归 MCP 协议使用）。
var logger = log.New(os.Stderr, "memoryd: ", log.LstdFlags)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, rest := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(rest)
	case "git-commit":
		err = cmdGitCommit(rest)
	case "stdio":
		err = cmdStdio(rest)
	case "rebuild-index":
		err = cmdRebuildIndex(rest)
	case "git-init":
		err = cmdGitInit(rest)
	case "search":
		err = cmdSearch(rest)
	case "version", "-version", "--version":
		fmt.Printf("zipper-agent-memoryd %s\n", version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "memoryd: unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "memoryd: %v\n", err)
		os.Exit(1)
	}
}

// newFlagSet 创建子命令 flag 集并注册通用 -root / -db 参数。
func newFlagSet(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	root := fs.String("root", "./memory", "记忆库根目录")
	db := fs.String("db", "", "索引数据库路径（默认 memory/ 同级 .zipper-agent-memory.index.sqlite）")
	return fs, root, db
}

// resolveDBPath 计算索引数据库路径：-db 未指定时放在 root 同级（绝对路径）。
func resolveDBPath(rootAbs, db string) string {
	if db != "" {
		return db
	}
	return filepath.Join(filepath.Dir(rootAbs), ".zipper-agent-memory.index.sqlite")
}

// openStoreIndex 打开记忆库与索引（serve/stdio 共用）。
func openStoreIndex(root, db string) (*memory.Store, *index.Index, string, error) {
	store, err := memory.OpenStore(root)
	if err != nil {
		return nil, nil, "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, "", err
	}
	rootAbs = filepath.Clean(rootAbs)
	ix, err := index.Open(resolveDBPath(rootAbs, db))
	if err != nil {
		return nil, nil, "", err
	}
	return store, ix, rootAbs, nil
}

// eventToIndex 返回 watcher 事件 → 索引的 handler（serve 共用；
// 阶段 2 逻辑：过滤可索引文件，按 Remove/Upsert 应用，单 goroutine 串行）。
func eventToIndex(ix *index.Index, rootAbs string) func([]watch.Event) {
	return func(evs []watch.Event) {
		for _, e := range evs {
			rel, err := filepath.Rel(rootAbs, e.Path)
			if err != nil || rel == "." {
				continue
			}
			rel = filepath.ToSlash(rel)
			if !index.ShouldIndex(rel, false) {
				continue
			}
			if e.Remove {
				if err := ix.Remove(rel); err != nil {
					logger.Printf("index remove %s: %v", rel, err)
				}
				continue
			}
			data, err := os.ReadFile(e.Path)
			if err != nil {
				if os.IsNotExist(err) {
					_ = ix.Remove(rel) // 读到即已删除：按删除处理
				} else {
					logger.Printf("read %s: %v", e.Path, err)
				}
				continue
			}
			info, err := os.Stat(e.Path)
			if err != nil {
				continue
			}
			m, body := index.ParseFrontMatter(data)
			m.MTime = info.ModTime()
			m.Size = info.Size()
			if err := ix.Upsert(rel, m, []byte(body)); err != nil {
				logger.Printf("index upsert %s: %v", rel, err)
			}
		}
	}
}

// serveFlags 承载 serve 子命令的专属 flag 值（root/db 由 newFlagSet 提供）。
type serveFlags struct {
	debounce time.Duration
	addr     string
	gitAuto  bool
	allowIPs string
}

// registerServeFlags 注册 serve 专属 flag 并返回取值容器。独立成函数供测试
// 解析同一注册逻辑断言默认值（避免测试中重复定义导致 flag 漂移）。
func registerServeFlags(fs *flag.FlagSet) *serveFlags {
	f := &serveFlags{}
	fs.DurationVar(&f.debounce, "debounce", 500*time.Millisecond, "文件事件去抖窗口（滑动窗口，事件静默该时长后批量入库）")
	fs.StringVar(&f.addr, "addr", "127.0.0.1:8931", "MCP streamable HTTP 监听地址（host:port）")
	fs.BoolVar(&f.gitAuto, "git-autocommit", true, "git autocommit：每日 0 点定时对 memory/ 仓库 git add -A && commit 一次（默认开启，design 决策 3；可随时用 git-commit 子命令主动提交；仓库未初始化时自动 git init 并设置本地 user.name/email，不碰全局配置）")
	fs.StringVar(&f.allowIPs, "allow-ips", "", "MCP HTTP 访问 IP 白名单（逗号分隔，如 1.2.3.4,5.6.7.8；空=不限制，design 决策 6）")
	return f
}

// newServeAutoCommitter 依据 -git-autocommit 构造每日定时 AutoCommitter；
// 关闭时返回 (nil, nil)，serve 不启动定时 goroutine。
func newServeAutoCommitter(rootAbs string, enabled bool) (*git.AutoCommitter, error) {
	if !enabled {
		return nil, nil
	}
	ac, err := git.NewAutoCommitter(git.Options{
		Root:    rootAbs,
		Enabled: true,
		Logf:    logger.Printf,
	})
	if err != nil {
		return nil, fmt.Errorf("git autocommit: %w", err)
	}
	return ac, nil
}

// parseIPList 把 -allow-ips 的逗号分隔字符串拆成 IP 列表（去空白、去空项）。
func parseIPList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdServe(args []string) error {
	fs, root, db := newFlagSet("serve")
	sf := registerServeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zipper-agent-memoryd serve [-root dir] [-db path] [-debounce duration] [-addr host:port] [-git-autocommit[=bool]] [-allow-ips list]")
	}

	store, ix, rootAbs, err := openStoreIndex(*root, *db)
	if err != nil {
		return err
	}
	defer ix.Close()
	dbPath := resolveDBPath(rootAbs, *db)

	ctx, stop := signalContext()
	defer stop()

	// git autocommit：默认开启（决策 3），独立 goroutine 每日 0 点定时提交
	// 一次；watcher 去抖批次内不再直接提交——定时提交以整棵树为对象
	// （git add -A），幂等，与事件批次解耦（天然无反馈环）。
	ac, err := newServeAutoCommitter(rootAbs, sf.gitAuto)
	if err != nil {
		return err
	}
	acErr := make(chan error, 1)
	if ac != nil {
		logger.Printf("git autocommit enabled: daily commit at 00:00 local (repo=%s)", rootAbs)
		go func() { acErr <- ac.RunDaily(ctx, 0) }()
	}

	// 启动时全量重建：索引与磁盘一致后再开始增量监听（derived state 的
	// 一致点；后续不一致都可随时 rebuild 恢复）。
	n, err := ix.Rebuild(store)
	if err != nil {
		return fmt.Errorf("rebuild index: %w", err)
	}
	logger.Printf("index ready: %d files (db=%s)", n, dbPath)

	// watcher 去抖批次 Handler：只负责索引增量维护（git 提交与事件批次
	// 解耦，由上方每日定时 goroutine 负责，见 design.md §10 决策 3）。
	handler := eventToIndex(ix, rootAbs)

	w, err := watch.New(rootAbs, handler, watch.Options{
		Debounce: sf.debounce,
		OnError:  func(err error) { logger.Printf("watch: %v", err) },
	})
	if err != nil {
		return err
	}

	// MCP server：与 watcher/索引同一进程（design.md §4.1 方案 A）。
	mcpSrv := mcp.New(store, ix)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpSrv.Handler())

	// IP 白名单（决策 6）：空 = 不限制（本地 127.0.0.1 模式无感知）；
	// 非法条目在启动时快速失败，不留带病运行的半配置。
	ips := parseIPList(sf.allowIPs)
	allowList, err := mcp.NewIPAllowList(ips)
	if err != nil {
		return err
	}
	if !allowList.Empty() {
		logger.Printf("MCP IP allowlist enabled: %d entry(ies)", len(ips))
	}
	httpSrv := &http.Server{Addr: sf.addr, Handler: allowList.Middleware(mux)}

	httpErr := make(chan error, 1)
	go func() { httpErr <- httpSrv.ListenAndServe() }()
	logger.Printf("MCP server listening on http://%s/mcp (root=%s, debounce=%s)", sf.addr, rootAbs, sf.debounce)

	watchErr := make(chan error, 1)
	go func() { watchErr <- w.Start(ctx) }()

	// 任一组件异常退出或收到信号：优雅关闭 HTTP，取消 watcher 与每日提交。
	var (
		runErr error
		acDone bool
	)
	select {
	case err := <-httpErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("http server: %w", err)
		}
	case err := <-watchErr:
		runErr = err
	case err := <-acErr:
		acDone = true
		if err != nil {
			runErr = fmt.Errorf("git autocommit daily: %w", err)
		}
	case <-ctx.Done():
		logger.Printf("signal received, shutting down")
	}
	stop() // 取消 watcher 与每日提交 context

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Printf("http shutdown: %v", err)
	}
	if ac != nil && !acDone {
		<-acErr // 等待每日提交 goroutine 优雅退出（ctx 取消后立即返回）
	}
	if runErr != nil {
		return runErr
	}
	logger.Printf("shutdown complete")
	return nil
}

// cmdGitCommit 立即执行一次 git 提交（design.md §10 决策 3 的用户主动触发
// 入口，可随时运行）。仓库未初始化时自动 git init（与 serve 定时提交同语义）；
// 无变更时输出跳过。输出经 stdout（命令式输出，与 search 同约定）。
func cmdGitCommit(args []string) error {
	fs, root, _ := newFlagSet("git-commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zipper-agent-memoryd git-commit [-root dir]")
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	rootAbs = filepath.Clean(rootAbs)
	ac, err := git.NewAutoCommitter(git.Options{
		Root:    rootAbs,
		Enabled: true,
		Logf:    logger.Printf,
	})
	if err != nil {
		return err
	}
	committed, err := ac.Commit()
	if err != nil {
		return err
	}
	if committed {
		fmt.Printf("git-commit: committed changes in %s\n", rootAbs)
	} else {
		fmt.Printf("git-commit: no changes to commit in %s\n", rootAbs)
	}
	return nil
}

func cmdStdio(args []string) error {
	fs, root, db := newFlagSet("stdio")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zipper-agent-memoryd stdio [-root dir] [-db path]")
	}

	store, ix, rootAbs, err := openStoreIndex(*root, *db)
	if err != nil {
		return err
	}
	defer ix.Close()

	// 按需拉起时一次全量重建，保证检索基线一致（索引是 derived state）。
	n, err := ix.Rebuild(store)
	if err != nil {
		return fmt.Errorf("rebuild index: %w", err)
	}
	logger.Printf("stdio MCP server ready: %d files indexed (root=%s)", n, rootAbs)

	mcpSrv := mcp.New(store, ix)
	ctx, stop := signalContext()
	defer stop()
	return mcpSrv.RunStdio(ctx)
}

func cmdRebuildIndex(args []string) error {
	fs, root, db := newFlagSet("rebuild-index")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zipper-agent-memoryd rebuild-index [-root dir] [-db path]")
	}
	store, err := memory.OpenStore(*root)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	ix, err := index.Open(resolveDBPath(filepath.Clean(rootAbs), *db))
	if err != nil {
		return err
	}
	defer ix.Close()
	n, err := ix.Rebuild(store)
	if err != nil {
		return err
	}
	fmt.Printf("rebuild-index: %d files indexed\n", n)
	return nil
}

// cmdGitInit 初始化 memory/ 为 git 仓库并补全本地 user.name/email（幂等）。
// 全程只写仓库本地 config（git config --local），不碰全局配置。
func cmdGitInit(args []string) error {
	fs, root, _ := newFlagSet("git-init")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zipper-agent-memoryd git-init [-root dir]")
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	rootAbs = filepath.Clean(rootAbs)
	if err := git.EnsureRepo(rootAbs); err != nil {
		return err
	}
	logger.Printf("git repository ready at %s (local user.name=%s, user.email=%s)", rootAbs, git.DefaultUserName, git.DefaultUserEmail)
	fmt.Printf("git-init: repository ready at %s\n", rootAbs)
	return nil
}

func cmdSearch(args []string) error {
	fs, root, db := newFlagSet("search")
	limit := fs.Int("limit", 20, "最多返回命中条数")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: zipper-agent-memoryd search [-root dir] [-db path] [-limit n] \"query\"")
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	ix, err := index.Open(resolveDBPath(filepath.Clean(rootAbs), *db))
	if err != nil {
		return err
	}
	defer ix.Close()
	results, err := ix.Search(fs.Arg(0), *limit)
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Printf("%s: %s\n", r.Path, r.Snippet)
	}
	fmt.Printf("%d hit(s)\n", len(results))
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `zipper-agent-memoryd - ZipperAgentMemory 记忆守护进程（阶段 2/3/4 + 决策 3/6）

用法：
  zipper-agent-memoryd <command> [flags] [args]

命令：
  serve          常驻运行：全量重建索引 + fsnotify 监听 + MCP streamable HTTP
                （默认 http://127.0.0.1:8931/mcp，Ctrl+C 退出）
  git-commit     立即执行一次 git 提交（主动触发入口，无变更时输出跳过）
  stdio          MCP stdio 模式：按需拉起，供纯 stdio 客户端（客户端断开即退出）
  rebuild-index  全量重建索引后退出
  git-init       初始化 memory/ 为 git 仓库并补全本地 user.name/email（幂等）
  search         全文检索并输出 路径+命中片段
  version        输出版本号

通用 flag：
  -root <dir>  记忆库根目录（默认 ./memory）
  -db <path>   索引数据库路径（默认 memory/ 同级 .zipper-agent-memory.index.sqlite）

serve 专属 flag：
  -addr <host:port>   MCP 监听地址（默认 127.0.0.1:8931）
  -debounce <dur>     文件事件去抖窗口（默认 500ms）
  -git-autocommit     git autocommit：每日 0 点定时 git add -A && commit 一次
                      （默认开启；可随时用 git-commit 主动提交；仓库未初始化
                      时自动 git init，不碰全局配置）
  -allow-ips <list>   MCP HTTP 访问 IP 白名单（逗号分隔，如 1.2.3.4,5.6.7.8；
                      空=不限制）

示例：
  zipper-agent-memoryd serve -root memory
  zipper-agent-memoryd serve -root memory -addr 0.0.0.0:8931 -allow-ips "1.2.3.4,5.6.7.8"
  zipper-agent-memoryd serve -root memory -git-autocommit=false
  zipper-agent-memoryd git-commit -root memory
  zipper-agent-memoryd stdio -root memory
  zipper-agent-memoryd rebuild-index -root memory
  zipper-agent-memoryd git-init -root memory
  zipper-agent-memoryd search -root memory "Go 语言"
`)
}
