package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/azazo1/qrcode-cli/internal/decode"
	"github.com/azazo1/qrcode-cli/internal/imageio"
	"github.com/azazo1/qrcode-cli/internal/source"
)

// decodeOptions 是解码子命令的选项.
type decodeOptions struct {
	// JSON 输出机器可读的结构化结果.
	JSON bool
	// Text 只输出每个二维码的原始内容, 每行一条.
	Text bool
	// NoPreprocess 关闭放大与反色等图像预处理.
	NoPreprocess bool
}

// jsonReport 是 --json 输出的顶层结构.
//
// 顶层带 schema 版本号, 后续调整字段时使用方可以据此判断兼容性.
type jsonReport struct {
	Version int          `json:"version"`
	Sources []jsonSource `json:"sources"`
}

// jsonSource 是单张图片的解码结果.
type jsonSource struct {
	Source string     `json:"source"`
	Format string     `json:"format"`
	Width  int        `json:"width"`
	Height int        `json:"height"`
	Codes  []jsonCode `json:"codes"`
}

// jsonCode 是单个二维码.
type jsonCode struct {
	Index   int         `json:"index"`
	Text    string      `json:"text"`
	ECLevel string      `json:"ec_level,omitempty"`
	Bounds  decode.Rect `json:"bounds"`
}

// jsonSchemaVersion 是 --json 输出的结构版本.
const jsonSchemaVersion = 1

// runDecode 执行解码流程.
//
// inputs 可能包含多张图片 (多个文件参数时), 结果按输入顺序汇总.
func runDecode(ctx context.Context, stdout, stderr io.Writer, opts decodeOptions, inputs []source.Input) int {
	decoder := decode.New(decode.Options{NoPreprocess: opts.NoPreprocess})

	report := jsonReport{Version: jsonSchemaVersion}
	total := 0
	started := time.Now()

	for _, input := range inputs {
		if input.Kind != source.KindImage {
			fmt.Fprintf(stderr, "跳过非图片输入 %s\n", input.Label)
			continue
		}

		img, format, err := imageio.Decode(input.Data)
		if err != nil {
			slog.Debug("解码图片失败", "source", input.Origin.Code(), "label", input.Label, "error", err)
			fmt.Fprintf(stderr, "解码图片 %s 失败: %v\n", input.Label, err)
			return exitUsage
		}

		width, height := imageio.Size(img)
		slog.Debug("开始识别",
			"source", input.Origin.Code(),
			"label", input.Label,
			"format", string(format),
			"width", width,
			"height", height)

		identifyStart := time.Now()
		results, err := decoder.Decode(ctx, img)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Debug("识别被中断")
				return exitInterrupted
			}
			slog.Debug("识别失败", "label", input.Label, "error", err)
			fmt.Fprintf(stderr, "识别 %s 失败: %v\n", input.Label, err)
			return exitUsage
		}

		slog.Debug("识别完成",
			"label", input.Label,
			"codes", len(results),
			"elapsed", time.Since(identifyStart))

		// 位置与纠错等级属于诊断信息, 只在 -v 或 --json 下出现.
		if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			for i, r := range results {
				slog.Debug("识别到二维码",
					"index", i+1,
					"ec_level", r.ECLevel,
					"x", r.Bounds.X, "y", r.Bounds.Y,
					"width", r.Bounds.Width, "height", r.Bounds.Height,
					"length", len(r.Text))
			}
		}

		src := jsonSource{
			Source: input.Origin.Code(),
			Format: string(format),
			Width:  width,
			Height: height,
			Codes:  make([]jsonCode, 0, len(results)),
		}
		for i, r := range results {
			src.Codes = append(src.Codes, jsonCode{
				Index:   i + 1,
				Text:    r.Text,
				ECLevel: r.ECLevel,
				Bounds:  r.Bounds,
			})
		}
		report.Sources = append(report.Sources, src)
		total += len(results)

		if opts.JSON || opts.Text {
			continue
		}
		printHuman(stdout, input, results, len(inputs) > 1)
	}

	slog.Debug("解码流程结束",
		"images", len(report.Sources),
		"codes", total,
		"elapsed", time.Since(started))

	if opts.JSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "输出 JSON 失败: %v\n", err)
			return exitUsage
		}
	} else if opts.Text {
		printPlain(stdout, report)
	}

	if total == 0 {
		fmt.Fprintln(stderr, "没有找到二维码")
		return exitNoCode
	}
	return exitOK
}

// printHuman 按便于阅读的形式打印结果.
//
// 单个码只打印内容; 多个码编号列出, 便于对应图片里的位置.
func printHuman(stdout io.Writer, input source.Input, results []decode.Result, showLabel bool) {
	if showLabel {
		fmt.Fprintf(stdout, "== %s ==\n", input.Label)
	}
	if len(results) == 0 {
		if showLabel {
			fmt.Fprintln(stdout)
		}
		return
	}

	if len(results) == 1 {
		fmt.Fprintln(stdout, results[0].Text)
		return
	}

	for i, r := range results {
		fmt.Fprintf(stdout, "[%d] %s\n", i+1, r.Text)
	}
	if showLabel {
		fmt.Fprintln(stdout)
	}
}

// printPlain 只输出二维码内容, 每行一条, 供管道使用.
func printPlain(stdout io.Writer, report jsonReport) {
	for _, src := range report.Sources {
		for _, c := range src.Codes {
			fmt.Fprintln(stdout, c.Text)
		}
	}
}

// 退出码约定.
const (
	exitOK          = 0
	exitUsage       = 1
	exitNoCode      = 2
	exitInterrupted = 130
)
