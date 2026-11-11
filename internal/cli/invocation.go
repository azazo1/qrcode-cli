package cli

import (
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/azazo1/qrcode-cli/internal/source"
)

// 处理模式的稳定标识, 用于日志.
const (
	modeDecode = "decode"
	modeEncode = "encode"
)

// 判断方式的稳定标识, 用于日志.
//
// auto 表示没有写子命令, 由程序按输入内容自行判定;
// explicit 表示用户写了 d/e 子命令.
const (
	byAuto     = "auto"
	byExplicit = "explicit"
)

// contentPreviewLimit 是日志里展示输入内容的最大字符数.
//
// 日志一行放得下才便于扫一眼, 超长的文本截断成前缀;
// 完整内容由标准输出负责, 日志只用来确认"读到的确实是这份数据".
const contentPreviewLimit = 120

// logInvocation 输出本次调用读取了什么, 按什么模式处理, 结果写到哪里.
//
// 这条日志默认开启: 自动模式下程序会替用户决定解码还是编码, 来源也可能是
// 剪贴板或管道, 没有这条日志用户就不知道刚才发生了什么. 它写到 stderr,
// 因此不会污染 stdout 上的二维码内容或 PNG 二进制.
//
// 每个输入来源各记一行, 多文件解码时能看清分别是哪几张图.
func logInvocation(by, mode, output string, inputs []source.Input) {
	for _, in := range inputs {
		attrs := []any{
			"mode", mode,
			"by", by,
			"origin", in.Origin.Code(),
			"source", in.Label,
			"kind", in.Kind.String(),
			// content 让日志直接显示读到的东西本身, 而不只是一个来源名称.
			"content", describeContent(in),
		}
		// 图片的格式已经在 content 里说明, 不再单独重复一个 format 字段.
		attrs = append(attrs, "bytes", len(in.Data), "output", output)

		slog.Info("输入源与模式", attrs...)
	}
}

// describeContent 给出输入内容的可读摘要.
//
// 文本显示内容本身; 图片是无法直接阅读的二进制, 显示格式即可,
// 字节数由调用处统一附加.
func describeContent(in source.Input) string {
	if in.Kind == source.KindImage {
		if in.Format == "" {
			return "未知格式"
		}
		return string(in.Format)
	}
	return previewText(string(in.Data))
}

// previewText 把文本压成一行摘要, 超出上限时截断.
func previewText(text string) string {
	runes := []rune(text)
	if len(runes) <= contentPreviewLimit {
		return text
	}

	truncated := string(runes[:contentPreviewLimit])
	return truncated + fmt.Sprintf("...(共 %d 字符)", utf8.RuneCountInString(text))
}
