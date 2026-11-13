package cli

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/spf13/pflag"

	"github.com/azazo1/qrcode-cli/internal/cimbar"
)

// cimbarOptions 是 cimbar 页面传输子命令的选项.
type cimbarOptions struct {
	// Bind 是网页服务的监听地址.
	Bind string
	// OutputDir 是接收到的文件保存目录.
	OutputDir string
	// NoOpen 不在启动服务后自动打开浏览器.
	NoOpen bool
	// Verbose 输出调试日志.
	Verbose bool
	// Help 显示帮助.
	Help bool
}

// registerCimbar 把 cimbar 子命令的选项挂到 flag 集合上.
//
// port 与编码 / 摄像头子命令一样在解析后换算进 Bind, 由调用处单独定义.
func registerCimbar(fs *pflag.FlagSet, opts *cimbarOptions) {
	fs.StringVar(&opts.Bind, "bind", "127.0.0.1:0", "网页服务监听地址")
	fs.StringVarP(&opts.OutputDir, "output", "o", ".", "接收到的文件保存目录")
	fs.BoolVar(&opts.NoOpen, "no-open", false, "启动网页服务后不自动打开浏览器")
	fs.BoolVarP(&opts.Verbose, "verbose", "v", false, "输出调试日志")
	fs.BoolVarP(&opts.Help, "help", "h", false, "显示帮助")
}

// runCimbarCommand 启动 cimbar 的本地页面, 用屏幕与摄像头搬运大文件.
//
// 发送页与接收页都由同一个服务提供, 接收页还原出来的文件直接写到
// --output 指定的目录, 不经过浏览器的下载目录. 服务常驻到按 Ctrl+C.
func (r *Runner) runCimbarCommand(ctx context.Context, args []string) int {
	opts := cimbarOptions{OutputDir: "."}
	fs := newFlagSet("qr cimbar")
	registerCimbar(fs, &opts)
	port := fs.Int("port", 0, "网页服务端口, 0 表示自动分配")
	if err := fs.Parse(args); err != nil {
		return r.failUsage(err)
	}
	if opts.Help {
		r.printUsage()
		return exitOK
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(r.Stderr, "cimbar 不接受位置参数: %s\n", fs.Arg(0))
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

	server, url, err := cimbar.Start(opts.Bind, cimbar.Options{
		OutputDir: outDir,
		OnSave: func(saved cimbar.Saved) {
			fmt.Fprintf(r.Stderr, "已保存: %s (%d 字节)\n", saved.Path, saved.Size)
		},
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "启动 cimbar 服务失败: %v\n", err)
		return exitUsage
	}

	slog.Info("cimbar 服务已启动", "url", url, "output", outDir)
	fmt.Fprintf(r.Stderr, "cimbar 页面: %s\n", url)
	fmt.Fprintf(r.Stderr, "发送页面: %sindex.html\n", url)
	fmt.Fprintf(r.Stderr, "接收页面: %srecv.html\n", url)
	fmt.Fprintf(r.Stderr, "接收到的文件保存到: %s\n", outDir)

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

	fmt.Fprintln(r.Stderr, "发送端在页面里选择文件并播放图块, 接收端允许摄像头权限后对准屏幕")
	fmt.Fprintln(r.Stderr, "按 Ctrl+C 结束服务")

	if err := server.Wait(ctx); err != nil {
		fmt.Fprintf(r.Stderr, "%v\n", err)
		return exitUsage
	}

	saved := server.Saved()
	slog.Info("cimbar 服务结束", "files", len(saved))
	if len(saved) == 0 {
		fmt.Fprintln(r.Stderr, "没有接收到文件")
		return exitNoCode
	}

	for _, f := range saved {
		if _, err := fmt.Fprintln(r.Stdout, f.Path); err != nil {
			fmt.Fprintf(r.Stderr, "写出结果失败: %v\n", err)
			return exitUsage
		}
	}
	return exitOK
}
