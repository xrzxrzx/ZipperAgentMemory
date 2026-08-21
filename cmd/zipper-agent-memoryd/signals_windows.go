//go:build windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalContext 返回在 Ctrl+C（SIGINT）或 SIGTERM 时取消的 context。
// Windows 下 SIGTERM 由 Go runtime 模拟支持（Notify 可注册）；
// SIGQUIT/SIGHUP 在 Windows 无对应语义，不注册。
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
