// Package logging 负责初始化全局结构化日志.
// 日志一律写到 stderr, 避免污染 stdout 上真正的结果输出.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Setup 初始化全局日志.
//
// 默认级别是 Info: 每次调用都会记一行"读到了什么, 按什么模式处理, 结果写到哪里",
// 因为自动模式会替用户决定解码还是编码, 输入也可能来自剪贴板或管道,
// 不记这一行用户就不知道刚才发生了什么.
//
// -v 时降到 Debug, 补充每个输入的处理细节与耗时.
func Setup(verbose bool, out io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	if out == nil {
		out = os.Stderr
	}

	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: compactAttr,
	}))
	slog.SetDefault(logger)
	return logger
}

// compactAttr 精简默认输出里对命令行场景意义不大的字段.
//
// 这里是短命进程, 完整日期时间只是噪声, 因此只保留时分秒;
// 级别名转成小写, 与常见的命令行日志风格一致.
func compactAttr(groups []string, a slog.Attr) slog.Attr {
	// 只处理顶层属性, 组内的键名保持原样.
	if len(groups) != 0 {
		return a
	}

	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.String(slog.TimeKey, t.Format("15:04:05"))
		}
	case slog.LevelKey:
		if level, ok := a.Value.Any().(slog.Level); ok {
			return slog.String(slog.LevelKey, strings.ToLower(level.String()))
		}
	}
	return a
}
