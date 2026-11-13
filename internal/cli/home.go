package cli

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/spf13/pflag"

	"github.com/azazo1/qrcode-cli/internal/cimbar"
	"github.com/azazo1/qrcode-cli/internal/home"
)

// homeOptions 是统一入口服务的选项.
type homeOptions struct {
	// Bind 是网页服务的监听地址.
	Bind string
	// OutputDir 是 cimbar 接收文件的保存目录.
	OutputDir string
	// NoOpen 不在启动服务后自动打开浏览器.
	NoOpen bool
	// Verbose 输出调试日志.
	Verbose bool
	// Help 显示帮助.
	Help bool
}

// registerHome 把统一入口服务的选项挂到 flag 集合上.
//
// port 与其它网页子命令一样在解析后换算进 Bind, 由调用处单独定义.
func registerHome(fs *pflag.FlagSet, opts *homeOptions) {
	fs.StringVar(&opts.Bind, "bind", "127.0.0.1:0", "网页服务监听地址")
	fs.StringVarP(&opts.OutputDir, "output", "o", ".", "cimbar 接收文件的保存目录")
	fs.BoolVar(&opts.NoOpen, "no-open", false, "启动网页服务后不自动打开浏览器")
	fs.BoolVarP(&opts.Verbose, "verbose", "v", false, "输出调试日志")
	fs.BoolVarP(&opts.Help, "help", "h", false, "显示帮助")
}

// runHomeCommand 启动统一入口服务: 一个页面里进入编码, 解码, 摄像头与 cimbar.
//
// 服务常驻到按 Ctrl+C; 退出时把摄像头识别到的内容逐行写到标准输出,
// cimbar 接收到的文件则在落盘时打印路径.
func (r *Runner) runHomeCommand(ctx context.Context, args []string) int {
	opts := homeOptions{OutputDir: "."}
	fs := newFlagSet("qr home")
	registerHome(fs, &opts)
	port := fs.Int("port", 0, "网页服务端口, 0 表示自动分配")
	if err := fs.Parse(args); err != nil {
		return r.failUsage(err)
	}
	if opts.Help {
		r.printUsage()
		return exitOK
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(r.Stderr, "入口服务不接受位置参数: %s\n", fs.Arg(0))
		return exitUsage
	}
	if *port != 0 {
		opts.Bind = fmt.Sprintf("127.0.0.1:%d", *port)
	}

	r.setupLogging(opts.Verbose)

	outDir, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, "解析输出目录失败: %v\n", err)
		return exitUsage
	}

	server, url, err := home.Start(opts.Bind, home.Options{
		OutputDir: outDir,
		OnSave: func(saved cimbar.Saved) {
			fmt.Fprintf(r.Stderr, "已保存: %s (%d 字节)\n", saved.Path, saved.Size)
		},
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "启动入口服务失败: %v\n", err)
		return exitUsage
	}

	slog.Info("入口服务已启动", "url", url, "output", outDir)
	fmt.Fprintf(r.Stderr, "入口页面: %s\n", url)
	fmt.Fprintf(r.Stderr, "cimbar 接收到的文件保存到: %s\n", outDir)

	// 打开浏览器失败不算错误: 服务已经在跑, 用户仍可以手动打开那个地址.
	if opts.NoOpen {
		slog.Info("已跳过自动打开浏览器", "reason", "--no-open")
	} else if err := r.openURL(url); err != nil {
		slog.Warn("自动打开浏览器失败, 请手动访问上面的地址", "error", err)
		fmt.Fprintf(r.Stderr, "自动打开浏览器失败: %v\n", err)
	} else {
		slog.Info("已用默认浏览器打开", "url", url)
		fmt.Fprintln(r.Stderr, "已在浏览器中打开")
	}
	fmt.Fprintln(r.Stderr, "按 Ctrl+C 结束服务")

	if err := server.Wait(ctx); err != nil {
		fmt.Fprintf(r.Stderr, "%v\n", err)
		return exitUsage
	}

	return r.printHomeSummary(server)
}

// printHomeSummary 输出本次会话里摄像头识别到的内容.
//
// cimbar 的文件在落盘时就打印过路径, 这里不重复输出, 只记一条统计日志.
func (r *Runner) printHomeSummary(server *home.Server) int {
	hits := server.Cam().Hits()
	saved := server.Cimbar().Saved()
	slog.Info("入口服务结束", "hits", len(hits), "files", len(saved))

	for _, text := range hits {
		if _, err := fmt.Fprintln(r.Stdout, text); err != nil {
			fmt.Fprintf(r.Stderr, "写出识别结果失败: %v\n", err)
			return exitUsage
		}
	}
	return exitOK
}
