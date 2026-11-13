// Package cli 实现命令行入口: 参数解析, 模式判定与结果输出.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/azazo1/qrcode-cli/internal/buildinfo"
	"github.com/azazo1/qrcode-cli/internal/clipboard"
	"github.com/azazo1/qrcode-cli/internal/encode"
	"github.com/azazo1/qrcode-cli/internal/logging"
	"github.com/azazo1/qrcode-cli/internal/source"
)

// Runner 是一次命令执行所需的全部外部依赖.
//
// 把 stdin/stdout/stderr 与终端判定都做成字段, 测试就能完整驱动一遍命令,
// 不必真的读写终端.
type Runner struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// StdinIsTerminal 报告标准输入是否连接终端.
	StdinIsTerminal bool
	// StdoutIsTerminal 覆盖"标准输出是否为终端"的判定.
	// 为 nil 时按 Stdout 是否为终端文件推断; 测试用它模拟真实终端.
	StdoutIsTerminal *bool
	// TerminalSize 覆盖终端宽高; 为 nil 时读取真实终端, 失败则按 80x24.
	TerminalSize *[2]int
	// LogOutput 是日志的输出目标; 为 nil 时写 Stderr.
	// 测试用它把日志收进缓冲区, 既不污染测试输出, 也能断言日志内容.
	LogOutput io.Writer
	// OpenURL 用系统浏览器打开地址; 为 nil 时调用系统默认浏览器.
	// 测试用它记录被打开的地址, 避免测试真的弹出浏览器窗口.
	OpenURL func(string) error
	// Env 是输入源解析依赖, 留空时使用真实环境.
	Env *source.Env
}

// Run 解析参数并执行, 返回进程退出码.
func (r *Runner) Run(ctx context.Context, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			r.printUsage()
			return exitOK
		case "--version", "version":
			fmt.Fprintln(r.Stdout, buildinfo.Version())
			return exitOK
		}
	}

	// 先看有没有子命令, 有则剥掉再解析该子命令的选项.
	mode := ""
	rest := args
	if len(args) > 0 {
		if normalized := normalizeCommand(args[0]); normalized != "" {
			mode = normalized
			rest = args[1:]
		}
	}

	switch mode {
	case "decode":
		return r.runDecodeCommand(ctx, rest)
	case "encode":
		return r.runEncodeCommand(ctx, rest)
	case "cam":
		return r.runCamCommand(ctx, rest)
	case "cimbar":
		return r.runCimbarCommand(ctx, rest)
	case "home":
		return r.runHomeCommand(ctx, rest)
	default:
		return r.runAutoCommand(ctx, rest)
	}
}

// commonFlags 是父子命令共用的选项.
type commonFlags struct {
	input   string
	output  string
	verbose bool
	help    bool
}

// registerCommon 把共用选项挂到 flag 集合上.
func registerCommon(fs *pflag.FlagSet, cf *commonFlags) {
	fs.StringVarP(&cf.input, "input", "i", "", "输入来源: - 表示标准输入, clipboard 表示剪贴板, 其他值按路径处理")
	fs.StringVarP(&cf.output, "output", "o", "", "输出路径")
	fs.BoolVarP(&cf.verbose, "verbose", "v", false, "输出调试日志")
	fs.BoolVarP(&cf.help, "help", "h", false, "显示帮助")
}

// newFlagSet 创建一个静默的 flag 集合, 用法与错误由我们自己输出.
//
// 与标准库 flag 不同, pflag 允许选项出现在位置参数之后,
// 因此 qr e "文本" --json 这种常见写法也能正确解析.
func newFlagSet(name string) *pflag.FlagSet {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// runAutoCommand 处理没有子命令的情形: 依据输入判定到底该解码还是编码.
func (r *Runner) runAutoCommand(ctx context.Context, args []string) int {
	cf := commonFlags{}
	fs := newFlagSet("qr")
	registerCommon(fs, &cf)
	if err := fs.Parse(args); err != nil {
		return r.failUsage(err)
	}
	if cf.help {
		r.printUsage()
		return exitOK
	}

	r.setupLogging(cf.verbose)
	env := r.env()

	inputs, err := r.collectAuto(env, &cf, fs.Args())
	if err != nil {
		return r.failInput(err)
	}

	// 所有输入都是图片时按解码处理, 否则按文本编码.
	allImages := len(inputs) > 0
	for _, in := range inputs {
		if in.Kind != source.KindImage {
			allImages = false
			break
		}
	}

	if allImages {
		return r.runDecode(ctx, byAuto, decodeOptions{}, inputs)
	}

	return r.runEncode(ctx, byAuto, defaultEncodeOptions(cf.output), mergeText(inputs))
}

// runDecodeCommand 处理显式解码.
func (r *Runner) runDecodeCommand(ctx context.Context, args []string) int {
	cf := commonFlags{}
	opts := decodeOptions{}
	fs := newFlagSet("qr decode")
	registerCommon(fs, &cf)
	fs.BoolVar(&opts.JSON, "json", false, "以 JSON 输出结果")
	fs.BoolVar(&opts.Text, "text", false, "只输出二维码内容, 每行一条")
	fs.BoolVar(&opts.NoPreprocess, "no-preprocess", false, "关闭全部图像预处理与增强, 只做单次识别")
	if err := fs.Parse(args); err != nil {
		return r.failUsage(err)
	}
	if cf.help {
		r.printUsage()
		return exitOK
	}

	r.setupLogging(cf.verbose)
	env := r.env()

	inputs, err := r.collectDecode(env, &cf, fs.Args())
	if err != nil {
		return r.failInput(err)
	}
	if len(inputs) == 0 {
		return r.failInput(source.ErrNoInput)
	}
	return r.runDecode(ctx, byExplicit, opts, inputs)
}

// collectDecode 收集解码输入.
//
// 与通用的 collect 不同, 这里对输入更严格: 解码的对象必然是图片,
// 因此文件不存在, 内容不是图片, 剪贴板里只有文本, 都直接报错而不是静默跳过.
// 静默跳过会让用户看到 "没有找到二维码", 误以为是图片里没有码.
func (r *Runner) collectDecode(env source.Env, cf *commonFlags, args []string) ([]source.Input, error) {
	switch {
	case cf.input == "clipboard":
		return readClipboardImage(env)
	case cf.input == "-":
		input, err := env.FromStdin()
		if err != nil {
			return nil, err
		}
		if input.Kind != source.KindImage {
			return nil, fmt.Errorf("标准输入的内容不是图片")
		}
		return []source.Input{input}, nil
	case cf.input != "":
		return readImageFiles(env, []string{cf.input})
	case len(args) > 0:
		return readImageFiles(env, args)
	case !env.StdinIsTerminal:
		input, err := env.FromStdin()
		if err != nil {
			// 管道里没有数据时退回剪贴板, 与自动模式的判定保持一致.
			if errors.Is(err, source.ErrNoInput) {
				return readClipboardImage(env)
			}
			return nil, err
		}
		if input.Kind != source.KindImage {
			return nil, fmt.Errorf("标准输入的内容不是图片")
		}
		return []source.Input{input}, nil
	default:
		return readClipboardImage(env)
	}
}

// readClipboardImage 严格读取剪贴板图片, 没有图片时给出明确提示.
//
// 解码场景不接受剪贴板文本, 否则用户会看到 "没有找到二维码" 这种误导性结论.
func readClipboardImage(env source.Env) ([]source.Input, error) {
	input, err := env.FromClipboardImage()
	if err != nil {
		if errors.Is(err, clipboard.ErrImageUnavailable) {
			return nil, fmt.Errorf("剪贴板中没有图片")
		}
		if errors.Is(err, clipboard.ErrUnsupported) {
			return nil, fmt.Errorf("当前环境无法读取剪贴板, 请改用文件或管道输入")
		}
		return nil, err
	}
	return []source.Input{input}, nil
}

// readImageFiles 逐个读取图片文件, 遇到非图片内容立即报错.
func readImageFiles(env source.Env, args []string) ([]source.Input, error) {
	inputs := make([]source.Input, 0, len(args))
	for _, arg := range args {
		if _, err := env.Stat(arg); err != nil {
			return nil, fmt.Errorf("文件 %s 不存在或无法访问", arg)
		}

		input, err := env.FromArgs([]string{arg})
		if err != nil {
			return nil, err
		}
		if input.Kind != source.KindImage {
			return nil, fmt.Errorf("%s 不是可识别的图片", arg)
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

// runEncodeCommand 处理显式编码.
func (r *Runner) runEncodeCommand(ctx context.Context, args []string) int {
	cf := commonFlags{}
	opts := defaultEncodeOptions("")
	fs := newFlagSet("qr encode")
	registerCommon(fs, &cf)
	level := fs.StringP("level", "l", "M", "纠错等级: L, M, Q, H")
	fs.IntVarP(&opts.ModuleSize, "size", "s", 8, "PNG 每模块像素数 (1..64)")
	fs.BoolVar(&opts.NoColor, "no-color", false, "终端输出不带颜色")
	fs.BoolVar(&opts.ForceWeb, "web", false, "强制在浏览器中显示")
	fs.BoolVar(&opts.NoOpen, "no-open", false, "启动网页服务后不自动打开浏览器")
	fs.BoolVar(&opts.ForceTerminal, "terminal", false, "强制输出终端字符画, 即使标准输出不是终端")
	fs.StringVar(&opts.Bind, "bind", "127.0.0.1:0", "网页服务监听地址")
	port := fs.Int("port", 0, "网页服务端口, 0 表示自动分配")
	if err := fs.Parse(args); err != nil {
		return r.failUsage(err)
	}
	if cf.help {
		r.printUsage()
		return exitOK
	}

	parsed, err := encode.ParseLevel(*level)
	if err != nil {
		fmt.Fprintf(r.Stderr, "%v\n", err)
		return exitUsage
	}
	opts.Level = parsed
	opts.Output = cf.output

	if *port != 0 {
		opts.Bind = fmt.Sprintf("127.0.0.1:%d", *port)
	}

	r.setupLogging(cf.verbose)
	env := r.env()

	inputs, err := r.collect(env, &cf, fs.Args())
	if err != nil {
		return r.failInput(err)
	}
	if len(inputs) == 0 {
		return r.failInput(source.ErrNoInput)
	}

	return r.runEncode(ctx, byExplicit, opts, mergeText(inputs))
}

// mergeText 把多份输入合并成一份文本输入.
// 编码时多个来源的内容按空格拼接, 与多个位置参数的处理保持一致.
func mergeText(inputs []source.Input) source.Input {
	if len(inputs) == 0 {
		return source.Input{Kind: source.KindText}
	}
	if len(inputs) == 1 {
		return inputs[0]
	}

	parts := make([]string, 0, len(inputs))
	for _, in := range inputs {
		parts = append(parts, string(in.Data))
	}
	return source.Input{
		Origin: inputs[0].Origin,
		Label:  inputs[0].Label,
		Kind:   source.KindText,
		Data:   []byte(strings.Join(parts, " ")),
	}
}

// defaultEncodeOptions 返回编码选项的默认值.
func defaultEncodeOptions(output string) encodeOptions {
	return encodeOptions{
		Level:      encode.LevelMedium,
		ModuleSize: 8,
		Bind:       "127.0.0.1:0",
		Output:     output,
	}
}

// setupLogging 初始化日志, 输出目标默认是 Stderr.
//
// 日志必须与业务输出分开: 结果可能写到 stdout 的二维码内容或 PNG 二进制,
// 日志混进去会破坏管道. 测试通过 LogOutput 把日志收进缓冲区.
func (r *Runner) setupLogging(verbose bool) {
	out := r.LogOutput
	if out == nil {
		out = r.Stderr
	}
	logging.Setup(verbose, out)
}

// env 构造输入源依赖, 注入标准输入与终端状态.
func (r *Runner) env() source.Env {
	if r.Env != nil {
		return *r.Env
	}
	env := source.DefaultEnv(r.StdinIsTerminal)
	if r.Stdin != nil {
		env.Stdin = r.Stdin
	}
	return env
}

// collect 按显式来源或位置参数收集输入.
func (r *Runner) collect(env source.Env, cf *commonFlags, args []string) ([]source.Input, error) {
	switch {
	case cf.input == "clipboard":
		return collectClipboard(env)
	case cf.input == "-":
		input, err := env.FromStdin()
		if err != nil {
			return nil, err
		}
		return []source.Input{input}, nil
	case cf.input != "":
		return collectFiles(env, []string{cf.input})
	case len(args) > 0:
		return collectFiles(env, args)
	case !env.StdinIsTerminal:
		input, err := env.FromStdin()
		if err != nil {
			return nil, err
		}
		return []source.Input{input}, nil
	default:
		input, err := env.Auto(nil)
		if err != nil {
			return nil, err
		}
		return []source.Input{input}, nil
	}
}

// collectClipboard 读取剪贴板, 优先图片, 没有图片时退回文本.
func collectClipboard(env source.Env) ([]source.Input, error) {
	input, err := env.FromClipboardImage()
	if err == nil {
		return []source.Input{input}, nil
	}
	if errors.Is(err, clipboard.ErrImageUnavailable) || errors.Is(err, clipboard.ErrUnsupported) {
		text, textErr := env.FromClipboardText()
		if textErr != nil {
			return nil, textErr
		}
		return []source.Input{text}, nil
	}
	return nil, err
}

// collectFiles 逐个读取文件参数, 保留每个文件独立的解码结果.
func collectFiles(env source.Env, args []string) ([]source.Input, error) {
	inputs := make([]source.Input, 0, len(args))
	for _, arg := range args {
		input, err := env.FromArgs([]string{arg})
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

// collectAuto 处理自动模式下的输入收集.
//
// 与 collect 的差别在于: 自动模式对多个位置参数不做逐个文件读取, 而是交给
// source.FromArgs 统一判定, 保证 "qr a b" 被当作一段文本.
func (r *Runner) collectAuto(env source.Env, cf *commonFlags, args []string) ([]source.Input, error) {
	if cf.input != "" {
		return r.collect(env, cf, args)
	}

	input, err := env.Auto(args)
	if err != nil {
		return nil, err
	}
	return []source.Input{input}, nil
}

// failUsage 输出参数解析错误并附上帮助提示.
func (r *Runner) failUsage(err error) int {
	fmt.Fprintf(r.Stderr, "%v\n", err)
	fmt.Fprintln(r.Stderr, "使用 qr -h 查看帮助")
	return exitUsage
}

// failInput 输出输入错误; 没有输入时额外提示用法.
func (r *Runner) failInput(err error) int {
	fmt.Fprintf(r.Stderr, "%v\n", err)
	if errors.Is(err, source.ErrNoInput) {
		fmt.Fprintln(r.Stderr, "使用 qr -h 查看帮助")
	}
	return exitUsage
}

// printUsage 打印帮助, 含各选项的实际定义.
func (r *Runner) printUsage() {
	decodeFS, encodeFS, camFS, commonFS := newFlagSet("decode"), newFlagSet("encode"), newFlagSet("cam"), newFlagSet("common")
	cimbarFS := newFlagSet("cimbar")
	homeFS := newFlagSet("home")
	opts := decodeOptions{}
	enc := defaultEncodeOptions("")
	camOpts := camOptions{}
	cimbarOpts := cimbarOptions{OutputDir: "."}
	homeOpts := homeOptions{OutputDir: "."}
	cf1, cf2, cf3 := commonFlags{}, commonFlags{}, commonFlags{}

	registerCommon(decodeFS, &cf1)
	decodeFS.BoolVar(&opts.JSON, "json", false, "以 JSON 输出结果")
	decodeFS.BoolVar(&opts.Text, "text", false, "只输出内容")
	decodeFS.BoolVar(&opts.NoPreprocess, "no-preprocess", false, "关闭全部图像预处理与增强, 只做单次识别")

	registerCommon(encodeFS, &cf2)
	encodeFS.StringP("level", "l", "M", "纠错等级")
	encodeFS.IntVarP(&enc.ModuleSize, "size", "s", 8, "PNG 每模块像素数")
	encodeFS.BoolVar(&enc.NoColor, "no-color", false, "终端输出不带颜色")
	encodeFS.BoolVar(&enc.ForceWeb, "web", false, "强制在浏览器中显示")
	encodeFS.BoolVar(&enc.NoOpen, "no-open", false, "不自动打开浏览器")
	encodeFS.BoolVar(&enc.ForceTerminal, "terminal", false, "强制输出终端字符画")
	encodeFS.StringVar(&enc.Bind, "bind", "127.0.0.1:0", "监听地址")
	encodeFS.Int("port", 0, "网页端口")

	registerCam(camFS, &camOpts)
	camFS.Int("port", 0, "网页服务端口")

	registerCimbar(cimbarFS, &cimbarOpts)
	cimbarFS.Int("port", 0, "网页服务端口")

	registerHome(homeFS, &homeOpts)
	homeFS.Int("port", 0, "网页服务端口")

	registerCommon(commonFS, &cf3)

	fmt.Fprint(r.Stdout, usage(
		describeFlags(decodeFS),
		describeFlags(encodeFS),
		describeFlags(camFS),
		describeFlags(cimbarFS),
		describeFlags(homeFS),
		describeFlags(commonFS),
	))
}

// describeFlags 把 flag 集合格式化成缩进后的选项说明.
func describeFlags(fs *pflag.FlagSet) string {
	var b strings.Builder
	fs.VisitAll(func(f *pflag.Flag) {
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		usage := f.Usage
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			usage += fmt.Sprintf(" (默认 %s)", f.DefValue)
		}
		fmt.Fprintf(&b, "  %-22s %s\n", name, usage)
	})
	return b.String()
}

// Main 是供 cmd 包调用的入口, 统一处理信号与退出码.
func Main(ctx context.Context, args []string, stdinIsTerminal bool) int {
	runner := &Runner{
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		StdinIsTerminal: stdinIsTerminal,
	}
	return runner.Run(ctx, args)
}
