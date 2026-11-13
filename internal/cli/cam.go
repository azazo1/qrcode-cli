package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/pflag"

	"github.com/azazo1/qrcode-cli/internal/cam"
	"github.com/azazo1/qrcode-cli/internal/decode"
)

// camOptions 是摄像头扫码子命令的选项.
type camOptions struct {
	// Bind 是网页服务的监听地址.
	Bind string
	// NoOpen 不在启动服务后自动打开浏览器.
	NoOpen bool
	// NoPreprocess 关闭图像预处理与增强, 与解码子命令的含义一致.
	NoPreprocess bool
	// Verbose 输出调试日志.
	Verbose bool
	// Help 显示帮助.
	Help bool
}

// registerCam 把摄像头扫码的选项挂到 flag 集合上.
//
// port 不在这里注册: 它需要在解析后换算进 Bind, 由调用处单独定义,
// 与编码子命令的处理保持一致.
func registerCam(fs *pflag.FlagSet, opts *camOptions) {
	fs.StringVar(&opts.Bind, "bind", "127.0.0.1:0", "网页服务监听地址")
	fs.BoolVar(&opts.NoOpen, "no-open", false, "启动网页服务后不自动打开浏览器")
	fs.BoolVar(&opts.NoPreprocess, "no-preprocess", false, "关闭图像预处理与增强, 只做单次识别")
	fs.BoolVarP(&opts.Verbose, "verbose", "v", false, "输出调试日志")
	fs.BoolVarP(&opts.Help, "help", "h", false, "显示帮助")
}

// runCamCommand 用设备摄像头扫描二维码.
//
// 摄像头由浏览器页面调起, 识别仍在本地完成. 页面点"结束"或直接关掉标签页后
// 服务收尾, 已识别到的内容逐行写到标准输出.
func (r *Runner) runCamCommand(ctx context.Context, args []string) int {
	opts := camOptions{}
	fs := newFlagSet("qr cam")
	registerCam(fs, &opts)
	port := fs.Int("port", 0, "网页服务端口, 0 表示自动分配")
	if err := fs.Parse(args); err != nil {
		return r.failUsage(err)
	}
	if opts.Help {
		r.printUsage()
		return exitOK
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(r.Stderr, "摄像头扫码不接受位置参数: %s\n", fs.Arg(0))
		return exitUsage
	}
	if *port != 0 {
		opts.Bind = fmt.Sprintf("127.0.0.1:%d", *port)
	}

	r.setupLogging(opts.Verbose)

	server, url, err := cam.Start(opts.Bind, cam.Options{
		Decoder: decode.New(decode.Options{NoPreprocess: opts.NoPreprocess}),
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "启动摄像头扫码服务失败: %v\n", err)
		return exitUsage
	}

	slog.Info("摄像头扫码服务已启动", "url", url)
	fmt.Fprintf(r.Stderr, "摄像头扫码页面: %s\n", url)

	// 打开浏览器失败不算错误: 服务已经在跑, 用户仍可以手动打开那个地址.
	if opts.NoOpen {
		slog.Info("已跳过自动打开浏览器", "reason", "--no-open")
		fmt.Fprintln(r.Stderr, "按 Ctrl+C 结束服务")
	} else if err := r.openURL(url); err != nil {
		slog.Warn("自动打开浏览器失败, 请手动访问上面的地址", "error", err)
		fmt.Fprintf(r.Stderr, "自动打开浏览器失败: %v\n", err)
		fmt.Fprintln(r.Stderr, "按 Ctrl+C 结束服务")
	} else {
		slog.Info("已用默认浏览器打开", "url", url)
		fmt.Fprintln(r.Stderr, "已在浏览器中打开")
	}
	fmt.Fprintln(r.Stderr, "在页面里允许摄像头权限并对准二维码, 识别完成后点页面上的\"结束\"")

	if err := server.Wait(ctx); err != nil {
		fmt.Fprintf(r.Stderr, "%v\n", err)
		return exitUsage
	}

	hits := server.Hits()
	slog.Info("摄像头扫码结束", "frames", server.Frames(), "hits", len(hits))
	return r.printCamHits(hits)
}

// printCamHits 把识别到的内容逐行写到标准输出.
//
// 一条内容一行, 与解码子命令的 --text 输出保持一致, 方便接管道.
func (r *Runner) printCamHits(hits []string) int {
	if len(hits) == 0 {
		fmt.Fprintln(r.Stderr, "没有找到二维码")
		return exitNoCode
	}

	for _, text := range hits {
		if _, err := fmt.Fprintln(r.Stdout, text); err != nil {
			fmt.Fprintf(r.Stderr, "写出识别结果失败: %v\n", err)
			return exitUsage
		}
	}
	return exitOK
}
