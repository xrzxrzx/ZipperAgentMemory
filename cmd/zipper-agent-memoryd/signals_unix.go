//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalContext 返回在 SIGINT/SIGTERM 时取消的 context（Linux 目标环境，
// 常驻进程由 systemd 等以 SIGTERM 优雅停止）。
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
