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
	// ForceTerminal 即使标准输出不是终端也输出字符画.
	ForceTerminal bool
	// Bind 是网页服务的监听地址.
	Bind string
	// Output 是输出路径, 空表示写标准输出.
	Output string
}

// runEncode 执行编码流程.
//
// 输出目标按以下优先级决定:
//   - 指定了 -o 时写到文件, 格式按扩展名 (png 或 txt)
//   - 显式要求网页展示时启动网页服务
//   - 标准输出不是终端时输出 PNG 二进制
//   - 标准输出是终端时输出字符画; 放不下则改为网页展示
func (r *Runner) runEncode(ctx context.Context, opts encodeOptions, input source.Input) int {
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

	slog.Debug("编码完成",
		"length", len(content),
		"bytes", len(input.Data),
		"level", opts.Level,
		"version", m.Version,
		"modules", m.Size(),
		"source", input.Origin.Code())

	stdoutIsTerminal := r.stdoutIsTerminal() || opts.ForceTerminal

	// 显式输出到文件时遵守扩展名约定.
	if opts.Output != "" {
		return writeToFile(stderr, opts, m)
	}

	// 显式要求网页展示时优先级最高, 与标准输出是否为终端无关.
	if opts.ForceWeb {
		return r.serveWeb(ctx, opts, m)
	}

	if !stdoutIsTerminal {
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
	}

	// 强制字符画时不受终端尺寸限制, 由调用方自己承担显示不全的风险.
	if opts.ForceTerminal {
		fmt.Fprint(stdout, terminal.Render(m, !opts.NoColor))
		return exitOK
	}

	// 终端输出: 放得下就直接画, 放不下改为网页展示.
	termCols, termRows := r.terminalSize()
	if terminal.Fits(m, termCols, termRows) {
		fmt.Fprint(stdout, terminal.Render(m, !opts.NoColor))
		return exitOK
	}

	return r.serveWeb(ctx, opts, m)
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
	fmt.Fprintln(r.Stderr, "按 Ctrl+C 结束服务")
	slog.Info("网页服务已启动", "url", url, "modules", m.Size())

	if err := server.Wait(ctx); err != nil {
		fmt.Fprintf(r.Stderr, "%v\n", err)
	}
	return exitOK
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
