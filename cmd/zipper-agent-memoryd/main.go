// 命令 zipper-agent-memoryd 是 ZipperAgentMemory 的记忆守护进程入口。
//
// 子命令（阶段 2/3/4）：
//
//	serve          常驻运行：启动时全量重建索引，随后 fsnotify 监听 memory/
//	                （去抖合并）增量维护索引，并同时提供 MCP streamable HTTP
//	                服务（默认 http://127.0.0.1:8931/mcp）；可选 -git-autocommit
//	                在变更去抖批次后自动 git add -A && commit（默认关闭）；
//	                SIGINT/SIGTERM 优雅退出。
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
//	zipper-agent-memoryd serve -root memory -addr 127.0.0.1:8931 -git-autocommit
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

func cmdServe(args []string) error {
	fs, root, db := newFlagSet("serve")
	debounce := fs.Duration("debounce", 500*time.Millisecond, "文件事件去抖窗口（滑动窗口，事件静默该时长后批量入库）")
	addr := fs.String("addr", "127.0.0.1:8931", "MCP streamable HTTP 监听地址（host:port）")
	gitAuto := fs.Bool("git-autocommit", false, "启用 git autocommit：变更去抖批次后自动对 memory/ 仓库 git add -A && commit（默认关闭；仓库未初始化时自动 git init 并设置本地 user.name/email，不碰全局配置）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zipper-agent-memoryd serve [-root dir] [-db path] [-debounce duration] [-addr host:port] [-git-autocommit]")
	}

	store, ix, rootAbs, err := openStoreIndex(*root, *db)
	if err != nil {
		return err
	}
	defer ix.Close()
	dbPath := resolveDBPath(rootAbs, *db)

	// 可选 git autocommit（默认关闭，显式 -git-autocommit 开启）。
	var ac *git.AutoCommitter
	if *gitAuto {
		ac, err = git.NewAutoCommitter(git.Options{
			Root:    rootAbs,
			Enabled: true,
			Logf:    logger.Printf,
		})
		if err != nil {
			return fmt.Errorf("git autocommit: %w", err)
		}
		logger.Printf("git autocommit enabled (repo=%s)", rootAbs)
	}

	// 启动时全量重建：索引与磁盘一致后再开始增量监听（derived state 的
	// 一致点；后续不一致都可随时 rebuild 恢复）。
	n, err := ix.Rebuild(store)
	if err != nil {
		return fmt.Errorf("rebuild index: %w", err)
	}
	logger.Printf("index ready: %d files (db=%s)", n, dbPath)

	// watcher 去抖批次 Handler：索引更新后，若启用 autocommit 且批次含
	// .git 之外的变更，同步提交（与索引同一 goroutine，天然串行）。
	handler := eventToIndex(ix, rootAbs)
	if ac != nil {
		inner := handler
		handler = func(evs []watch.Event) {
			inner(evs)
			if hasNonGitEvent(rootAbs, evs) {
				if err := ac.Commit(); err != nil {
					logger.Printf("git autocommit: %v", err)
				}
			}
		}
	}

	w, err := watch.New(rootAbs, handler, watch.Options{
		Debounce: *debounce,
		OnError:  func(err error) { logger.Printf("watch: %v", err) },
	})
	if err != nil {
		return err
	}

	// MCP server：与 watcher/索引同一进程（design.md §4.1 方案 A）。
	mcpSrv := mcp.New(store, ix)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpSrv.Handler())
	httpSrv := &http.Server{Addr: *addr, Handler: mux}

	ctx, stop := signalContext()
	defer stop()

	httpErr := make(chan error, 1)
	go func() { httpErr <- httpSrv.ListenAndServe() }()
	logger.Printf("MCP server listening on http://%s/mcp (root=%s, debounce=%s)", *addr, rootAbs, *debounce)

	watchErr := make(chan error, 1)
	go func() { watchErr <- w.Start(ctx) }()

	// 任一组件异常退出或收到信号：优雅关闭 HTTP，取消 watcher。
	var runErr error
	select {
	case err := <-httpErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("http server: %w", err)
		}
	case err := <-watchErr:
		runErr = err
	case <-ctx.Done():
		logger.Printf("signal received, shutting down")
	}
	stop() // 取消 watcher context

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Printf("http shutdown: %v", err)
	}
	if runErr != nil {
		return runErr
	}
	logger.Printf("shutdown complete")
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

// hasNonGitEvent 报告批次中是否存在 memory/.git 之外的变更事件。
// git 自身写 .git 会触发 fsnotify 事件，若不滤除将形成
// 「提交 → 事件 → 提交」反馈环；git add -A 以整棵树为对象，
// 无需按事件定位变更，故 .git 内部批次可直接跳过。
func hasNonGitEvent(rootAbs string, evs []watch.Event) bool {
	gitDir := filepath.Join(rootAbs, ".git")
	for _, e := range evs {
		rel, err := filepath.Rel(gitDir, e.Path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue // 事件位于 .git 内
		}
		return true
	}
	return false
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
	fmt.Fprintf(os.Stderr, `zipper-agent-memoryd - ZipperAgentMemory 记忆守护进程（阶段 2/3/4）

用法：
  zipper-agent-memoryd <command> [flags] [args]

命令：
  serve          常驻运行：全量重建索引 + fsnotify 监听 + MCP streamable HTTP
                （默认 http://127.0.0.1:8931/mcp，Ctrl+C 退出）
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
  -git-autocommit     启用 git autocommit：变更去抖批次后自动 git add -A && commit
                      （默认关闭；仓库未初始化时自动 git init，不碰全局配置）

示例：
  zipper-agent-memoryd serve -root memory
  zipper-agent-memoryd serve -root memory -addr 127.0.0.1:8931 -git-autocommit
  zipper-agent-memoryd stdio -root memory
  zipper-agent-memoryd rebuild-index -root memory
  zipper-agent-memoryd git-init -root memory
  zipper-agent-memoryd search -root memory "Go 语言"
`)
}
