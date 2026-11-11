package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/azazo1/qrcode-cli/internal/browser"
	"github.com/azazo1/qrcode-cli/internal/encode"
	"github.com/azazo1/qrcode-cli/internal/source"
	"github.com/azazo1/qrcode-cli/internal/terminal"
	"github.com/azazo1/qrcode-cli/internal/webui"
)

// encodeOptions 是编码子命令的选项.
type encodeOptions struct {
	// Level 是纠错等级.
	Level encode.Level
	// ModuleSize 是 PNG 输出时每个模块的像素数.
	ModuleSize int
	// NoColor 关闭终端配色.
	NoColor bool
	// ForceWeb 强制在浏览器里显示.
	ForceWeb bool
	// NoOpen 不在启动网页服务后自动打开浏览器.
	NoOpen bool
	// ForceTerminal 即使标准输出不是终端也输出字符画.
	ForceTerminal bool
	// Bind 是网页服务的监听地址.
	Bind string
	// Output 是输出路径, 空表示写标准输出.
	Output string
}

// encodeTarget 是编码结果的实际去向.
//
// 把去向先解析成值再分别处理, 是为了让日志能如实报告结果去了哪里:
// 如果日志里另写一套判断, 就会和真正的分支逐渐偏离.
type encodeTarget int

const (
	targetPNGFile     encodeTarget = iota // 写入 PNG 文件
	targetTextFile                        // 写入字符画文本文件
	targetWeb                             // 本地网页服务
	targetStdoutPNG                       // 标准输出 PNG 二进制
	targetStdoutChars                     // 标准输出字符画
)

// describe 返回便于阅读的去向描述, 用于日志.
func (t encodeTarget) describe(opts encodeOptions) string {
	switch t {
	case targetPNGFile:
		return "file:" + opts.Output
	case targetTextFile:
		return "file:" + opts.Output + "(txt)"
	case targetWeb:
		// 这里不写监听端口: --port 0 时真实端口要等服务启动后才知道,
		// 写死配置值会报出一个并不存在的地址. 真实地址由 serveWeb 记录.
		return "web"
	case targetStdoutPNG:
		return "stdout(png)"
	case targetStdoutChars:
		return "stdout(chars)"
	default:
		return "unknown"
	}
}

// resolveTarget 决定编码结果写到哪里.
//
// 优先级: -o 指定的文件 > --web > 标准输出不是终端时写 PNG >
// --terminal 强制字符画 > 终端放得下就画字符画 > 放不下改用网页.
func (r *Runner) resolveTarget(opts encodeOptions, m *encode.Matrix) encodeTarget {
	if opts.Output != "" {
		if strings.ToLower(filepath.Ext(opts.Output)) == ".txt" {
			return targetTextFile
		}
		return targetPNGFile
	}

	if opts.ForceWeb {
		return targetWeb
	}

	// 标准输出不是终端且没有强制字符画时, 输出 PNG 二进制供管道或重定向使用.
	if !r.stdoutIsTerminal() && !opts.ForceTerminal {
		return targetStdoutPNG
	}

	if opts.ForceTerminal {
		return targetStdoutChars
	}

	cols, rows := r.terminalSize()
	if terminal.Fits(m, cols, rows) {
		return targetStdoutChars
	}
	return targetWeb
}

// runEncode 执行编码流程.
//
// by 说明这次编码是自动判定得出的还是显式指定的, 只用于日志.
func (r *Runner) runEncode(ctx context.Context, by string, opts encodeOptions, input source.Input) int {
	stdout, stderr := r.Stdout, r.Stderr

	// 编码只看文本内容; 图片输入在这里没有意义, 直接拒绝比猜测更清楚.
	if input.Kind == source.KindImage {
		fmt.Fprintf(stderr, "%q 是图片, 编码模式需要文本内容; 请用 qr e \"文本\"\n", input.Label)
		return exitUsage
	}

	content := string(input.Data)
	m, err := encode.Build(content, opts.Level)
	if err != nil {
		// 这是用户输入问题, 提示已经打印到 stderr, 日志只作为诊断记录.
		slog.Debug("编码失败", "length", len(content), "level", opts.Level, "error", err)
		fmt.Fprintf(stderr, "编码失败: %v\n", err)
		return exitUsage
	}

	target := r.resolveTarget(opts, m)
	logInvocation(by, modeEncode, target.describe(opts), []source.Input{input})

	slog.Debug("编码完成",
		"length", len(content),
		"bytes", len(input.Data),
		"level", opts.Level,
		"version", m.Version,
		"modules", m.Size(),
		"source", input.Origin.Code())

	switch target {
	case targetPNGFile, targetTextFile:
		return writeToFile(stderr, opts, m)
	case targetWeb:
		return r.serveWeb(ctx, opts, m)
	case targetStdoutPNG:
		pngData, err := m.RenderPNG(opts.ModuleSize)
		if err != nil {
			fmt.Fprintf(stderr, "生成 PNG 失败: %v\n", err)
			return exitUsage
		}
		if _, err := stdout.Write(pngData); err != nil {
			fmt.Fprintf(stderr, "写出 PNG 失败: %v\n", err)
			return exitUsage
		}
		return exitOK
	default:
		fmt.Fprint(stdout, terminal.Render(m, !opts.NoColor))
		return exitOK
	}
}

// writeToFile 把结果写入指定文件.
func writeToFile(stderr io.Writer, opts encodeOptions, m *encode.Matrix) int {
	ext := strings.ToLower(filepath.Ext(opts.Output))

	if ext == ".txt" {
		content := terminal.Render(m, false)
		if err := os.WriteFile(opts.Output, []byte(content), 0o644); err != nil {
			fmt.Fprintf(stderr, "写入 %s 失败: %v\n", opts.Output, err)
			return exitUsage
		}
		fmt.Fprintf(stderr, "已写入 %s\n", opts.Output)
		return exitOK
	}

	pngData, err := m.RenderPNG(opts.ModuleSize)
	if err != nil {
		fmt.Fprintf(stderr, "生成 PNG 失败: %v\n", err)
		return exitUsage
	}
	if err := os.WriteFile(opts.Output, pngData, 0o644); err != nil {
		fmt.Fprintf(stderr, "写入 %s 失败: %v\n", opts.Output, err)
		return exitUsage
	}
	fmt.Fprintf(stderr, "已写入 %s\n", opts.Output)
	return exitOK
}

// serveWeb 启动本地网页展示二维码, 直到收到中断信号.
func (r *Runner) serveWeb(ctx context.Context, opts encodeOptions, m *encode.Matrix) int {
	pngData, err := m.RenderPNG(opts.ModuleSize)
	if err != nil {
		fmt.Fprintf(r.Stderr, "生成 PNG 失败: %v\n", err)
		return exitUsage
	}

	server, url, err := webui.Start(opts.Bind, m, pngData)
	if err != nil {
		fmt.Fprintf(r.Stderr, "启动网页服务失败: %v\n", err)
		return exitUsage
	}

	fmt.Fprintf(r.Stderr, "二维码完整显示在 %s\n", url)

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
		fmt.Fprintln(r.Stderr, "已在浏览器中打开, 按 Ctrl+C 结束服务")
	}

	if err := server.Wait(ctx); err != nil {
		fmt.Fprintf(r.Stderr, "%v\n", err)
	}
	return exitOK
}

// openURL 用系统默认浏览器打开地址.
//
// Runner 可以注入自己的实现, 测试借此记录被打开的地址而不真的弹窗.
func (r *Runner) openURL(url string) error {
	if r.OpenURL != nil {
		return r.OpenURL(url)
	}
	return browser.Open(url)
}

// stdoutIsTerminal 判断标准输出是否为终端.
//
// 只对 *os.File 能给出可靠结论; 其他 writer (例如测试里的 buffer) 一律
// 视为非终端, 这样默认路径输出 PNG, 便于测试断言.
func (r *Runner) stdoutIsTerminal() bool {
	if r.StdoutIsTerminal != nil {
		return *r.StdoutIsTerminal
	}
	file, ok := r.Stdout.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

// terminalSize 返回终端宽高, 取不到时给一个保守的默认值.
func (r *Runner) terminalSize() (cols, rows int) {
	if r.TerminalSize != nil {
		return r.TerminalSize[0], r.TerminalSize[1]
	}
	if file, ok := r.Stdout.(*os.File); ok {
		if width, height, err := term.GetSize(int(file.Fd())); err == nil && width > 0 && height > 0 {
			return width, height
		}
	}
	// 默认按 80x24 计算, 这是最常见的终端尺寸.
	return 80, 24
}
