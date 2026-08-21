// 命令 zipper-agent-memoryd 是 ZipperAgentMemory 的记忆守护进程入口。
//
// 子命令（阶段 2）：
//
//	serve          常驻运行：启动时全量重建索引，随后 fsnotify 监听 memory/
//	                （去抖合并）增量维护索引；SIGINT/SIGTERM 优雅退出。
//	rebuild-index  全量重建索引后退出（索引是 derived state，可随时重建）。
//	search QUERY   全文检索并输出 路径+命中片段（供人工/测试）。
//	version        输出版本号。
//
// 用法示例：
//
//	zipper-agent-memoryd serve -root memory
//	zipper-agent-memoryd rebuild-index -root memory
//	zipper-agent-memoryd search -root memory "Go 语言"
//
// 索引数据库默认放在 memory/ 根目录的同级（不进入被监听的目录树，避免
// 索引写入自身触发监听事件形成反馈；也保持 memory/（git 仓库）干净），
// 可用 -db 覆盖。仅用标准库 flag，不引第三方 CLI 框架（任务书约束）。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"zipper-agent-memory/pkg/index"
	"zipper-agent-memory/pkg/memory"
	"zipper-agent-memory/pkg/watch"
)

// version 是守护进程版本号，对应 docs/design.md 的阶段交付规划。
const version = "v0.2.0"

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
	case "rebuild-index":
		err = cmdRebuildIndex(rest)
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

func cmdServe(args []string) error {
	fs, root, db := newFlagSet("serve")
	debounce := fs.Duration("debounce", 500*time.Millisecond, "文件事件去抖窗口（滑动窗口，事件静默该时长后批量入库）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zipper-agent-memoryd serve [-root dir] [-db path] [-debounce duration]")
	}

	store, err := memory.OpenStore(*root)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	rootAbs = filepath.Clean(rootAbs)
	dbPath := resolveDBPath(rootAbs, *db)

	ix, err := index.Open(dbPath)
	if err != nil {
		return err
	}
	defer ix.Close()

	// 启动时全量重建：索引与磁盘一致后再开始增量监听（derived state 的
	// 一致点；后续不一致都可随时 rebuild 恢复）。
	n, err := ix.Rebuild(store)
	if err != nil {
		return fmt.Errorf("rebuild index: %w", err)
	}
	logger.Printf("index ready: %d files (db=%s)", n, dbPath)

	// 事件 → 索引：过滤可索引文件，按 Remove/Upsert 应用（单 goroutine
	// 串行回调，索引写天然有序）。
	handler := func(evs []watch.Event) {
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

	w, err := watch.New(rootAbs, handler, watch.Options{
		Debounce: *debounce,
		OnError:  func(err error) { logger.Printf("watch: %v", err) },
	})
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()
	logger.Printf("watching %s (debounce=%s); Ctrl+C 退出", rootAbs, *debounce)

	if err := w.Start(ctx); err != nil {
		return err
	}
	logger.Printf("shutdown complete")
	return nil
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
	fmt.Fprintf(os.Stderr, `zipper-agent-memoryd - ZipperAgentMemory 记忆守护进程（阶段 2）

用法：
  zipper-agent-memoryd <command> [flags] [args]

命令：
  serve          常驻运行：启动全量重建索引 + fsnotify 监听增量维护（Ctrl+C 退出）
  rebuild-index  全量重建索引后退出
  search         全文检索并输出 路径+命中片段
  version        输出版本号

通用 flag：
  -root <dir>  记忆库根目录（默认 ./memory）
  -db <path>   索引数据库路径（默认 memory/ 同级 .zipper-agent-memory.index.sqlite）

示例：
  zipper-agent-memoryd serve -root memory
  zipper-agent-memoryd rebuild-index -root memory
  zipper-agent-memoryd search -root memory "Go 语言"
`)
}
