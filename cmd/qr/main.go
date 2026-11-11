// Command qr 是二维码编解码命令行工具.
//
// 三种输入来源 (位置参数, 标准输入, 系统剪贴板) 与两种模式 (解码, 编码)
// 由 internal/cli 统一调度; 这里只负责信号处理, 终端判定与退出码.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/azazo1/qrcode-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := cli.Main(ctx, os.Args[1:], term.IsTerminal(int(os.Stdin.Fd())))
	os.Exit(code)
}
