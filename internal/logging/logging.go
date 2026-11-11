// Package logging 负责初始化全局结构化日志.
// 日志一律写到 stderr, 避免污染 stdout 上真正的结果输出.
package logging

import (
	"log/slog"
	"os"
)

// Setup 按 verbose 选择日志级别并设置全局默认 logger.
// 默认只保留警告与错误, -v 时输出调试细节.
func Setup(verbose bool) *slog.Logger {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger
}
