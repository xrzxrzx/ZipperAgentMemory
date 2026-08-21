package main

import (
	"path/filepath"
	"testing"

	"zipper-agent-memory/pkg/watch"
)

// TestHasNonGitEvent 验证 .git 内部事件被滤除（防「git 提交 → fsnotify 事件
// → 再次提交」反馈环，见 cmdServe 的 autocommit 装配注释）：批次仅含 .git
// 内部事件时不应触发提交；含任何普通文件事件则触发。
func TestHasNonGitEvent(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")

	tests := []struct {
		name string
		evs  []watch.Event
		want bool
	}{
		{"空批次不触发", nil, false},
		{"git 内单文件不触发", []watch.Event{{Path: filepath.Join(gitDir, "index")}}, false},
		{"git 内深层对象不触发", []watch.Event{{Path: filepath.Join(gitDir, "objects", "ab", "cdef")}}, false},
		{"git 目录本身不触发", []watch.Event{{Path: gitDir}}, false},
		{"普通文件触发", []watch.Event{{Path: filepath.Join(root, "notes", "a.md")}}, true},
		{"git 事件混合普通事件触发", []watch.Event{
			{Path: filepath.Join(gitDir, "index")},
			{Path: filepath.Join(root, "notes", "a.md")},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasNonGitEvent(root, tt.evs); got != tt.want {
				t.Errorf("hasNonGitEvent(%v) = %v, want %v", tt.evs, got, tt.want)
			}
		})
	}
}
