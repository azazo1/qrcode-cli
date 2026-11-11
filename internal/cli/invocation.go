package cli

import (
	"log/slog"

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
		}
		if in.Format != "" {
			attrs = append(attrs, "format", string(in.Format))
		}
		attrs = append(attrs, "bytes", len(in.Data), "output", output)

		slog.Info("输入源与模式", attrs...)
	}
}
