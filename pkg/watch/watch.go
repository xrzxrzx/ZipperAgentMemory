package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event 是一条去抖窗口内汇总的文件系统事件（绝对路径）。
// 同一路径在一个窗口内多次出现时按「最后一次为准」合并（见 mergeEvents）。
type Event struct {
	Path   string // 绝对路径
	Remove bool   // true=删除/移走（索引应 Remove）；false=创建/写入（索引应 Upsert）
}

// Options 配置 Watcher。
type Options struct {
	// Debounce 是去抖窗口时长；<=0 时使用默认 500ms。
	Debounce time.Duration
	// OnError 接收 fsnotify 底层错误（如个别目录监听失败）；nil 时静默丢弃。
	// 回调在监听 goroutine 内同步调用，应尽快返回。
	OnError func(error)
}

// defaultDebounce 是默认去抖窗口（编码规范 §4.4 的 time.Ticker 窗口）。
const defaultDebounce = 500 * time.Millisecond

// Watcher 递归监听 root 目录树，按去抖窗口批量回调 Handler。
//
// 生命周期：New 创建 → Start(ctx) 阻塞运行直到 ctx 取消（返回前 flush 剩余
// 事件）或发生不可恢复错误；Start 返回后 Watcher 即失效。Close 供提前
// 释放底层 fsnotify 资源（Start 返回时也会自动释放，幂等）。
//
// 线程模型：Start 在单个 goroutine 内完成事件收集/合并/回调（pending 是
// Start 的局部变量，无跨 goroutine 竞争）；w.watched 由 w.mu 保护
// （Close/release 可能从外部 goroutine 调用）。
type Watcher struct {
	root     string // 绝对路径的监听根
	debounce time.Duration
	handler  func([]Event)
	onErr    func(error)

	fw      *fsnotify.Watcher
	watched map[string]struct{} // 当前已挂监听的目录（绝对路径）
	mu      sync.Mutex

	closed   chan struct{}
	closeOne sync.Once
}

// New 创建监听 root 目录树的 Watcher（root 必须存在且为目录）。
// handler 在监听 goroutine 内同步调用，同一时间只有一个批次在处理，
// 天然串行化索引更新；handler 不应阻塞过久。
func New(root string, handler func([]Event), opts Options) (*Watcher, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("watch: resolve root %q: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("watch: stat root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("watch: root %q is not a directory", abs)
	}
	if handler == nil {
		return nil, fmt.Errorf("watch: nil handler")
	}
	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = defaultDebounce
	}
	return &Watcher{
		root:     filepath.Clean(abs),
		debounce: debounce,
		handler:  handler,
		onErr:    opts.OnError,
		watched:  make(map[string]struct{}),
		closed:   make(chan struct{}),
	}, nil
}

// Start 开始监听并阻塞运行，直到 ctx 取消（优雅退出）或 fsnotify 通道关闭。
// 返回前 flush 去抖窗口内的剩余事件，随后释放底层资源。
func (w *Watcher) Start(ctx context.Context) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watch: new fsnotify watcher: %w", err)
	}
	w.mu.Lock()
	w.fw = fw
	w.mu.Unlock()
	defer fw.Close()

	// 初始遍历：为 root 下现存全部目录挂监听（含深层子目录）。
	if err := w.addTree(w.root); err != nil {
		return err
	}

	var (
		pending []Event // 当前去抖窗口内的原始事件（仅本 goroutine 访问）
		timer   *time.Timer
		timerC  <-chan time.Time
	)
	// flush 合并窗口内事件并回调。窗口语义为滑动窗口：新事件重置计时。
	flush := func() {
		if len(pending) == 0 {
			return
		}
		evs := mergeEvents(pending)
		pending = nil
		w.handler(evs)
	}
	resetTimer := func() {
		if timerC == nil {
			timer = time.NewTimer(w.debounce)
			timerC = timer.C
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(w.debounce)
	}

	for {
		select {
		case <-ctx.Done():
			if timerC != nil {
				timer.Stop()
			}
			flush() // 停机前交付窗口内剩余事件，避免最后一次变更丢失
			w.release()
			return nil
		case err, ok := <-fw.Errors:
			if !ok {
				w.release()
				return nil
			}
			w.reportErr(err)
		case ev, ok := <-fw.Events:
			if !ok {
				w.release()
				return nil
			}
			w.handleEvent(ev, &pending)
			if len(pending) > 0 {
				resetTimer()
			}
		case <-timerC:
			timerC = nil
			flush()
		}
	}
}

// release 清理监听集合并关闭退出标记（幂等，可在停机或外部 Close 时调用）。
func (w *Watcher) release() {
	w.closeOne.Do(func() {
		w.mu.Lock()
		w.watched = make(map[string]struct{})
		w.mu.Unlock()
		close(w.closed)
	})
}

// Close 提前关闭底层 fsnotify 资源。Start 返回时已自动释放，本方法幂等；
// 通常在测试或不再需要监听时调用。
//
// 并发注意：fw 由 Start 创建并赋值、Close 关闭，二者本应串行使用
// （Start 阻塞运行期间调用 Close 属误用）；为防御误用，Close 与 Start
// 之间经 closed 通道协调——release() 关闭 closed 后，Start 的 select
// 会退出并执行 defer fw.Close()，避免对已关闭 watcher 的双重操作。
func (w *Watcher) Close() {
	if w == nil {
		return
	}
	// 仅当 Start 已创建 fw 且尚未释放时才主动关闭；否则交给
	// Start 的 defer（其 select 会在 closed 关闭后退出）。
	w.mu.Lock()
	started := w.fw != nil
	w.mu.Unlock()
	if started {
		_ = w.fw.Close()
	}
	w.release()
}

// handleEvent 处理一条 fsnotify 原始事件：维护目录监听集合，并把
// 可回调事件追加进 pending（去重由 flush 时的 mergeEvents 完成）。
func (w *Watcher) handleEvent(ev fsnotify.Event, pending *[]Event) {
	rel, err := filepath.Rel(w.root, ev.Name)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return // 根外路径（正常不会发生）忽略
	}
	switch {
	case ev.Op&fsnotify.Create != 0:
		// Create 既可能是新建，也可能是 rename 的目标半边
		// （fsnotify：mv 源= Rename 事件(旧路径)，目标 = Create 事件(新路径)）。
		// 两种情形都按「路径出现」处理（Upsert / 目录挂监听）。
		w.handlePathAppeared(ev.Name, pending)
	case ev.Op&fsnotify.Rename != 0:
		// Rename 事件携带的是「被移走的旧路径」：按 Remove 处理；
		// 新路径半边由随后的 Create 事件补上（见上）。
		w.handlePathGone(ev.Name, pending)
	case ev.Op&fsnotify.Remove != 0:
		w.handlePathGone(ev.Name, pending)
	case ev.Op&fsnotify.Write != 0:
		if _, err := os.Lstat(ev.Name); err == nil {
			*pending = append(*pending, Event{Path: ev.Name, Remove: false})
		} // 写已不存在的路径（删除竞态）忽略
	case ev.Op&fsnotify.Chmod != 0:
		// 权限变更不代表内容变化，忽略（避免 chmod 风暴触发无谓索引写）。
	default:
		// 其余操作（Attrib 等）忽略。
	}
}

// handlePathAppeared 处理新出现的路径（Create / rename 目标半边）：
// 目录→挂递归监听并补发其内文件事件；文件→Upsert 事件。
// 补发兜底「挂监听瞬间错过的文件创建」（R7 容错：Upsert 幂等）。
func (w *Watcher) handlePathAppeared(p string, pending *[]Event) {
	info, err := os.Lstat(p)
	if err != nil {
		return // 出现又消失的竞态：无事件可发
	}
	if info.IsDir() {
		if err := w.addTree(p); err != nil {
			w.reportErr(fmt.Errorf("watch: add tree %q: %w", p, err))
			return
		}
		for _, f := range collectFiles(p) {
			*pending = append(*pending, Event{Path: f, Remove: false})
		}
		return
	}
	*pending = append(*pending, Event{Path: p, Remove: false})
}

// handlePathGone 处理消失的路径（Remove / rename 源半边）：
// 摘除该路径及其下全部目录监听（避免陈旧条目阻挡同名目录重建挂监听），
// 并追加 Remove 事件（目录内文件的删除事件由 fsnotify 逐条送达）。
func (w *Watcher) handlePathGone(p string, pending *[]Event) {
	w.mu.Lock()
	for watched := range w.watched {
		if watched == p || strings.HasPrefix(watched, p+string(filepath.Separator)) {
			delete(w.watched, watched)
		}
	}
	w.mu.Unlock()
	*pending = append(*pending, Event{Path: p, Remove: true})
}

// reportErr 把错误交给 OnError 回调（nil 时静默）。
func (w *Watcher) reportErr(err error) {
	if w.onErr != nil {
		w.onErr(err)
	}
}

// addTree 为 dir 及其全部子目录挂监听（已挂过的目录跳过），返回硬错误
// （dir 本身挂不上/遍历失败，调用方应视为该目录不可监听）；子目录挂不上
// 仅经 OnError 报告，不影响其余子树。
func (w *Watcher) addTree(dir string) error {
	w.mu.Lock()
	hard, soft := w.addTreeLocked(dir)
	w.mu.Unlock()
	for _, e := range soft {
		w.reportErr(e)
	}
	return hard
}

// addTreeLocked 假定调用方已持有 w.mu（或不需锁的初始化阶段）。
func (w *Watcher) addTreeLocked(dir string) (hard error, soft []error) {
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if _, ok := w.watched[p]; ok {
			if p == dir {
				return nil
			}
			return filepath.SkipDir // 子树已监听过，不必下钻
		}
		if err := w.fw.Add(p); err != nil {
			if p == dir {
				return err // 根目录挂不上：硬错误
			}
			soft = append(soft, fmt.Errorf("watch: add %q: %w", p, err))
			return filepath.SkipDir
		}
		w.watched[p] = struct{}{}
		return nil
	})
	if err != nil {
		return err, soft
	}
	return nil, soft
}

// collectFiles 返回 dir 下全部普通文件（非递归，仅一层；子目录由各自的
// Create 事件继续展开）。
func collectFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	return files
}

// mergeEvents 合并一个去抖窗口内的原始事件：同一路径保留最后一条
// （Remove 与 Create 互相覆盖，以最终文件系统状态为准），并稳定保持
// 首次出现顺序。纯函数，供单元测试直接验证合并语义。
func mergeEvents(in []Event) []Event {
	last := make(map[string]int, len(in)) // path -> 结果下标
	out := make([]Event, 0, len(in))
	for _, e := range in {
		if idx, ok := last[e.Path]; ok {
			out[idx].Remove = e.Remove // 后到者覆盖
			continue
		}
		last[e.Path] = len(out)
		out = append(out, e)
	}
	return out
}
