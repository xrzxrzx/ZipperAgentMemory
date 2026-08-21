package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMergeEvents(t *testing.T) {
	tests := []struct {
		name string
		in   []Event
		want []Event
	}{
		{"空输入", nil, nil},
		{"单事件原样", []Event{{Path: "a", Remove: false}}, []Event{{Path: "a", Remove: false}}},
		{"同路径去重保留首次出现", []Event{{Path: "a"}, {Path: "a"}}, []Event{{Path: "a"}}},
		{"Create→Remove 最后为准", []Event{{Path: "a", Remove: false}, {Path: "a", Remove: true}}, []Event{{Path: "a", Remove: true}}},
		{"Remove→Create 最后为准", []Event{{Path: "a", Remove: true}, {Path: "a", Remove: false}}, []Event{{Path: "a", Remove: false}}},
		{"多路径顺序稳定", []Event{{Path: "b"}, {Path: "a"}, {Path: "a", Remove: true}}, []Event{{Path: "b"}, {Path: "a", Remove: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeEvents(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("mergeEvents = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("mergeEvents[%d] = %+v, want %+v (all: %+v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

// collector 线程安全收集 handler 批次。
type collector struct {
	mu  sync.Mutex
	evs []Event
}

func (c *collector) add(evs []Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evs = append(c.evs, evs...)
}

func (c *collector) has(f func(Event) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.evs {
		if f(e) {
			return true
		}
	}
	return false
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.evs)
}

// waitFor 轮询条件直到超时（Windows fsnotify 事件时序有抖动，超时给足余量）。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout (%s) waiting for %s", timeout, msg)
}

// startWatcher 在 root 上启动去抖 100ms 的监听并返回 ctx 取消函数。
func startWatcher(t *testing.T, root string, c *collector) context.CancelFunc {
	t.Helper()
	w, err := New(root, c.add, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("watcher did not stop")
		}
	})
	return cancel
}

func TestWatcherCreateModifyDelete(t *testing.T) {
	root := t.TempDir()
	c := &collector{}
	startWatcher(t, root, c)

	f1 := filepath.Join(root, "notes1.md")

	// 新建文件 → Upsert 事件。
	if err := os.WriteFile(f1, []byte("hello 世界"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == f1 && !e.Remove })
	}, "create event for notes1.md")

	// 修改文件 → Upsert 事件（存在即可，去抖合并后可能只有一条）。
	if err := os.WriteFile(f1, []byte("hello 世界 again"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == f1 && !e.Remove })
	}, "modify event for notes1.md")

	// 删除文件 → Remove 事件。
	if err := os.Remove(f1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == f1 && e.Remove })
	}, "remove event for notes1.md")
}

func TestWatcherNewSubdirRecursive(t *testing.T) {
	root := t.TempDir()
	c := &collector{}
	startWatcher(t, root, c)

	// 运行时新建多层子目录并在其中建文件。
	sub := filepath.Join(root, "notes", "deep", "inner")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(sub, "newfile.md")
	if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 8*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == f && !e.Remove })
	}, "file inside newly created nested dir")
}

func TestWatcherDirRemoveFiresFileEvents(t *testing.T) {
	root := t.TempDir()
	c := &collector{}
	startWatcher(t, root, c)

	dir := filepath.Join(root, "d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "f.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == f && !e.Remove })
	}, "file inside new dir indexed")

	// 删除整棵目录：目录与其内文件都应产生 Remove 事件。
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 8*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == f && e.Remove })
	}, "remove event for file inside removed dir")
	waitFor(t, 5*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == dir && e.Remove })
	}, "remove event for removed dir")
}

func TestWatcherRenameEmitsRemoveAndCreate(t *testing.T) {
	root := t.TempDir()
	c := &collector{}
	startWatcher(t, root, c)

	oldPath := filepath.Join(root, "old.md")
	newPath := filepath.Join(root, "new.md")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == oldPath && !e.Remove })
	}, "initial create of old.md")

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	// 旧路径应收到 Remove，新路径应收到 Upsert（fsnotify 配对或双事件均覆盖）。
	waitFor(t, 8*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == oldPath && e.Remove })
	}, "remove event for renamed-away path")
	waitFor(t, 8*time.Second, func() bool {
		return c.has(func(e Event) bool { return e.Path == newPath && !e.Remove })
	}, "create event for renamed-to path")
}

func TestWatcherFlushesOnCancel(t *testing.T) {
	root := t.TempDir()
	c := &collector{}
	w, err := New(root, c.add, Options{Debounce: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Start(ctx) }()

	// 写入后立即取消：Start 返回前必须 flush 窗口内事件（不丢最后一次变更）。
	f := filepath.Join(root, "last.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // 让事件进入窗口但不足 200ms 触发 flush
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not stop after cancel")
	}
	if !c.has(func(e Event) bool { return e.Path == f && !e.Remove }) {
		t.Errorf("final flush lost event for %s (got %d events)", f, c.count())
	}
}
