// Package logging 负责初始化全局结构化日志.
// 日志一律写到 stderr, 避免污染 stdout 上真正的结果输出.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// contentKey 是输入内容字段的键名, 它在日志里用浅灰色显示.
//
// 日志行里 mode, origin, output 这些是判断"发生了什么"的关键信息,
// 而内容本身可能很长, 压成灰色后两者一眼就能分开.
const contentKey = "content"

// 浅灰色 (ANSI 亮黑) 与复位序列.
const (
	ansiGray  = "\x1b[90m"
	ansiReset = "\x1b[0m"
)

// Setup 初始化全局日志.
//
// 默认级别是 Info: 每次调用都会记一行"读到了什么, 按什么模式处理, 结果写到哪里",
// 因为自动模式会替用户决定解码还是编码, 输入也可能来自剪贴板或管道,
// 不记这一行用户就不知道刚才发生了什么.
//
// -v 时降到 Debug, 补充每个输入的处理细节与耗时.
//
// 输出目标是终端时, content 字段会显示为浅灰色; 被重定向到文件或管道时不加
// 颜色, 否则这些转义序列会混进文件内容. NO_COLOR 非空时同样不加.
func Setup(verbose bool, out io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	if out == nil {
		out = os.Stderr
	}

	logger := slog.New(newHandler(out, level, colorEnabled(out)))
	slog.SetDefault(logger)
	return logger
}

// colorEnabled 判断是否可以给日志加颜色.
//
// 只对真正的终端启用: 管道与文件里出现 ANSI 序列是污染,
// 测试用的缓冲区也属于非终端, 因此断言时不必关心颜色.
func colorEnabled(out io.Writer) bool {
	// 遵循 https://no-color.org 的约定: 该变量非空即关闭颜色.
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}

	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

// handler 是与 slog.TextHandler 同构的实现.
//
// 之所以不用 slog.TextHandler: 它会把含 ANSI 转义序列的字符串当成非打印字符
// 整体转义, 于是颜色码变成字面量 \x1b[90m 打了出来. 这里自己拼接输出,
// 就能只对 content 的值原样写入颜色序列.
type handler struct {
	mu     *sync.Mutex
	out    io.Writer
	level  slog.Level
	color  bool
	attrs  []slog.Attr
	groups []string
}

// newHandler 创建日志 handler.
func newHandler(out io.Writer, level slog.Level, color bool) *handler {
	return &handler{
		mu:    &sync.Mutex{},
		out:   out,
		level: level,
		color: color,
	}
}

// Enabled 报告该级别是否需要输出.
func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle 格式化并写出一条记录.
func (h *handler) Handle(_ context.Context, record slog.Record) error {
	var b strings.Builder

	// 时间是短命进程, 只保留时分秒, 完整日期在命令行场景里只是噪声.
	b.WriteString("time=")
	b.WriteString(record.Time.Format("15:04:05"))
	b.WriteString(" level=")
	b.WriteString(strings.ToLower(record.Level.String()))
	b.WriteString(" msg=")
	b.WriteString(quoteIfNeeded(record.Message))

	for _, attr := range h.attrs {
		h.appendAttr(&b, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(&b, attr)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, b.String())
	return err
}

// WithAttrs 返回带附加属性的 handler.
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

// WithGroup 返回带分组前缀的 handler.
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string{}, h.groups...), name)
	return &clone
}

// appendAttr 输出单个属性.
func (h *handler) appendAttr(b *strings.Builder, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	b.WriteByte(' ')

	// 分组属性展开成 group.key=value 的形式, 与 slog.TextHandler 一致.
	if attr.Value.Kind() == slog.KindGroup {
		prefix := strings.Join(append(h.groups, attr.Key), ".")
		for _, child := range attr.Value.Group() {
			if prefix != "" {
				child.Key = prefix + "." + child.Key
			}
			h.appendAttr(b, child)
		}
		return
	}

	if len(h.groups) > 0 {
		b.WriteString(strings.Join(h.groups, ".") + ".")
	}
	b.WriteString(attr.Key)
	b.WriteByte('=')

	value := formatValue(attr.Value)
	if h.color && attr.Key == contentKey {
		b.WriteString(ansiGray)
		b.WriteString(value)
		b.WriteString(ansiReset)
		return
	}
	b.WriteString(value)
}

// formatValue 把属性值转成文本.
//
// 含空格, 等号或不可打印字符的值加引号, 否则一条日志里相邻字段会粘在一起、
// 无法区分. 这条规则与 slog 一致.
func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return quoteIfNeeded(v.String())
	case slog.KindTime:
		return quoteIfNeeded(v.Time().Format(time.RFC3339))
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindBool:
		return strconv.FormatBool(v.Bool())
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'g', -1, 64)
	default:
		return quoteIfNeeded(v.String())
	}
}

// quoteIfNeeded 在必要的时候用引号包裹字符串.
func quoteIfNeeded(s string) string {
	if needsQuoting(s) {
		return strconv.Quote(s)
	}
	return s
}

// needsQuoting 判断字符串是否需要加引号.
//
// 空格, 等号和引号都会让字段边界变模糊; 控制字符 (含换行) 会破坏单行结构,
// 因此一并交给 strconv.Quote 处理.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r == '=' || r == '"' || r == ' ' {
			return true
		}
		if r < ' ' || r == 0x7f {
			return true
		}
	}
	return false
}
