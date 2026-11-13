package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/azazo1/qrcode-cli/internal/clipboard"
	"github.com/azazo1/qrcode-cli/internal/imageio"
	"github.com/azazo1/qrcode-cli/internal/source"
)

// qrPNG 生成一张真实的二维码 PNG, 用于区分"图片"与"文本"输入.
func qrPNG(t *testing.T, text string) []byte {
	t.Helper()
	data, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		t.Fatalf("生成二维码失败: %v", err)
	}
	return data
}

// fakeEnv 构造一个完全受控的输入源环境.
//
// 文件系统只认识 files 里的名字, 剪贴板行为也由测试指定.
func fakeEnv(files map[string][]byte, clipImage []byte, clipText string, stdin []byte, stdinIsTerminal bool) source.Env {
	return source.Env{
		Stdin:           bytes.NewReader(stdin),
		StdinIsTerminal: stdinIsTerminal,
		ReadClipboardImage: func() ([]byte, error) {
			if len(clipImage) == 0 {
				return nil, clipboard.ErrImageUnavailable
			}
			return clipImage, nil
		},
		ReadClipboardText: func() ([]byte, error) {
			return []byte(clipText), nil
		},
		ReadFile: func(path string) ([]byte, error) {
			data, ok := files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return data, nil
		},
		Stat: func(path string) (os.FileInfo, error) {
			if _, ok := files[path]; !ok {
				return nil, os.ErrNotExist
			}
			return fakeFileInfo{name: path}, nil
		},
	}
}

// fakeFileInfo 满足 os.FileInfo, 只需要名字与"不是目录".
type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o644 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

// TestAutoModeSelection 覆盖自动模式判定表的每一种情形.
func TestAutoModeSelection(t *testing.T) {
	png := qrPNG(t, "auto-mode-payload")
	files := map[string][]byte{
		"shot.png": png,
		"note.txt": []byte("file-text-content"),
	}

	cases := []struct {
		name            string
		args            []string
		env             source.Env
		wantKind        source.Kind
		wantOrigin      source.Origin
		wantPayloadHint string
	}{
		{
			name:            "图片文件解码",
			args:            []string{"shot.png"},
			env:             fakeEnv(files, nil, "", nil, true),
			wantKind:        source.KindImage,
			wantOrigin:      source.OriginFile,
			wantPayloadHint: "png",
		},
		{
			name:            "文本文件编码",
			args:            []string{"note.txt"},
			env:             fakeEnv(files, nil, "", nil, true),
			wantKind:        source.KindText,
			wantOrigin:      source.OriginFile,
			wantPayloadHint: "file-text-content",
		},
		{
			name:            "非文件参数按文本编码",
			args:            []string{"hello"},
			env:             fakeEnv(files, nil, "", nil, true),
			wantKind:        source.KindText,
			wantOrigin:      source.OriginArg,
			wantPayloadHint: "hello",
		},
		{
			name:            "管道图片自动解码",
			args:            nil,
			env:             fakeEnv(files, nil, "", png, false),
			wantKind:        source.KindImage,
			wantOrigin:      source.OriginStdin,
			wantPayloadHint: "png",
		},
		{
			name:            "管道文本自动编码",
			args:            nil,
			env:             fakeEnv(files, nil, "", []byte("pipe-text"), false),
			wantKind:        source.KindText,
			wantOrigin:      source.OriginStdin,
			wantPayloadHint: "pipe-text",
		},
		{
			name:            "终端无参数优先剪贴板图片",
			args:            nil,
			env:             fakeEnv(files, png, "clip-text", nil, true),
			wantKind:        source.KindImage,
			wantOrigin:      source.OriginClipboard,
			wantPayloadHint: "png",
		},
		{
			name:            "终端无参数退回剪贴板文本",
			args:            nil,
			env:             fakeEnv(files, nil, "clip-text", nil, true),
			wantKind:        source.KindText,
			wantOrigin:      source.OriginClipboard,
			wantPayloadHint: "clip-text",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inputs, err := (&Runner{Env: &c.env}).collectAuto(c.env, &commonFlags{}, c.args)
			if err != nil {
				t.Fatalf("collectAuto: %v", err)
			}
			if len(inputs) != 1 {
				t.Fatalf("输入数 = %d, want 1", len(inputs))
			}
			if inputs[0].Kind != c.wantKind {
				t.Fatalf("kind = %v, want %v", inputs[0].Kind, c.wantKind)
			}
			if inputs[0].Origin != c.wantOrigin {
				t.Fatalf("origin = %v, want %v", inputs[0].Origin, c.wantOrigin)
			}
			if c.wantPayloadHint == "png" {
				if format, ok := imageio.SniffFormat(inputs[0].Data); !ok || format != imageio.FormatPNG {
					t.Fatalf("期望 PNG 内容, 实际格式 = %v", format)
				}
			} else if string(inputs[0].Data) != c.wantPayloadHint {
				t.Fatalf("内容 = %q, want %q", string(inputs[0].Data), c.wantPayloadHint)
			}
		})
	}
}

// TestAutoNoInput 验证完全没有输入时报错而不是静默成功.
func TestAutoNoInput(t *testing.T) {
	env := fakeEnv(nil, nil, "", nil, true)
	_, err := env.Auto(nil)
	if err == nil {
		t.Fatal("没有输入时应返回错误")
	}
}

// TestMultiArgsJoin 验证多个位置参数按空格拼接成一段文本.
func TestMultiArgsJoin(t *testing.T) {
	env := fakeEnv(nil, nil, "", nil, true)
	input, err := env.FromArgs([]string{"hello", "world"})
	if err != nil {
		t.Fatalf("FromArgs: %v", err)
	}
	if string(input.Data) != "hello world" {
		t.Fatalf("内容 = %q, want %q", string(input.Data), "hello world")
	}
}

// runWith 在受控环境中执行一次命令, 返回退出码与输出.
func runWith(t *testing.T, args []string, env source.Env) (int, string, string) {
	t.Helper()

	code, out, errb, _ := runWithLog(t, args, env)
	return code, out, errb
}

// runWithLog 同 runWith, 额外返回日志内容.
//
// 日志走独立的缓冲区, 避免全局 logger 把测试输出弄脏.
func runWithLog(t *testing.T, args []string, env source.Env) (int, string, string, string) {
	t.Helper()

	var out, errb, logb bytes.Buffer
	r := &Runner{
		Stdout:          &out,
		Stderr:          &errb,
		LogOutput:       &logb,
		StdinIsTerminal: env.StdinIsTerminal,
		Env:             &env,
	}
	code := r.Run(context.Background(), args)
	return code, out.String(), errb.String(), logb.String()
}

// TestRunDecodeFromPipe 端到端验证 "管道图片 -> 自动解码".
func TestRunDecodeFromPipe(t *testing.T) {
	png := qrPNG(t, "piped-payload-5521")
	env := fakeEnv(nil, nil, "", png, false)

	code, out, errs := runWith(t, nil, env)
	if code != exitOK {
		t.Fatalf("退出码 = %d, want %d (stderr: %s)", code, exitOK, errs)
	}
	if got := trimNewline(out); got != "piped-payload-5521" {
		t.Fatalf("输出 = %q", got)
	}
}

// TestRunDecodeJSON 验证结构化输出里的来源标识与内容.
func TestRunDecodeJSON(t *testing.T) {
	png := qrPNG(t, "json-payload")
	files := map[string][]byte{"a.png": png}
	env := fakeEnv(files, nil, "", nil, true)

	code, out, errs := runWith(t, []string{"d", "a.png", "--json"}, env)
	if code != exitOK {
		t.Fatalf("退出码 = %d (stderr: %s)", code, errs)
	}
	for _, want := range []string{`"version": 1`, `"source": "file"`, "json-payload"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("JSON 输出缺少 %q:\n%s", want, out)
		}
	}
}

// TestRunDecodeNoCode 验证没有二维码时退出码为 2.
func TestRunDecodeNoCode(t *testing.T) {
	blank, err := blankPNG()
	if err != nil {
		t.Fatalf("生成空白图失败: %v", err)
	}
	files := map[string][]byte{"blank.png": blank}
	env := fakeEnv(files, nil, "", nil, true)

	code, _, _ := runWith(t, []string{"d", "blank.png"}, env)
	if code != exitNoCode {
		t.Fatalf("退出码 = %d, want %d", code, exitNoCode)
	}
}

// TestRunEncodeToPipe 验证编码在非终端输出时写 PNG 二进制.
func TestRunEncodeToPipe(t *testing.T) {
	env := fakeEnv(nil, nil, "", nil, true)

	code, out, errs := runWith(t, []string{"e", "encode-me"}, env)
	if code != exitOK {
		t.Fatalf("退出码 = %d (stderr: %s)", code, errs)
	}
	format, ok := imageio.SniffFormat([]byte(out))
	if !ok || format != imageio.FormatPNG {
		t.Fatalf("输出不是 PNG, 格式 = %v", format)
	}
}

// TestRunUnknownFlag 验证未知选项会失败并提示帮助.
func TestRunUnknownFlag(t *testing.T) {
	env := fakeEnv(nil, nil, "", nil, true)

	code, _, errs := runWith(t, []string{"e", "--not-a-flag"}, env)
	if code != exitUsage {
		t.Fatalf("退出码 = %d, want %d", code, exitUsage)
	}
	if !bytes.Contains([]byte(errs), []byte("-h")) {
		t.Fatalf("错误输出应提示帮助: %s", errs)
	}
}

// TestNormalizeCommand 校验子命令别名.
func TestNormalizeCommand(t *testing.T) {
	cases := map[string]string{
		"d":      "decode",
		"decode": "decode",
		"e":      "encode",
		"encode": "encode",
		"cam":    "cam",
		"camera": "cam",
		"CAMERA": "cam",
		"hello":  "",
	}
	for in, want := range cases {
		if got := normalizeCommand(in); got != want {
			t.Fatalf("normalizeCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAutoFallsBackToClipboardOnEmptyStdin 验证非终端但读不到数据时回退剪贴板.
//
// 脚本与部分 IDE 终端会把 stdin 接到 /dev/null, 此时 stdin 不是终端却没有任何
// 数据; 若只依据 "不是终端" 就认定有管道输入, 剪贴板里的内容永远用不上.
func TestAutoFallsBackToClipboardOnEmptyStdin(t *testing.T) {
	png := qrPNG(t, "clipboard-fallback-8831")
	env := fakeEnv(nil, png, "", nil, false)

	input, err := env.Auto(nil)
	if err != nil {
		t.Fatalf("Auto: %v", err)
	}
	if input.Kind != source.KindImage {
		t.Fatalf("kind = %v, want %v", input.Kind, source.KindImage)
	}
	if input.Origin != source.OriginClipboard {
		t.Fatalf("origin = %v, want %v", input.Origin, source.OriginClipboard)
	}
}

// TestAutoStdinErrorIsNotSwallowed 验证真实读取故障不会被剪贴板回退掩盖.
func TestAutoStdinErrorIsNotSwallowed(t *testing.T) {
	env := fakeEnv(nil, nil, "", nil, false)
	env.Stdin = errReader{}

	if _, err := env.Auto(nil); err == nil {
		t.Fatal("标准输入读取失败时应返回错误")
	}
}

// errReader 模拟读取失败的标准输入.
type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, os.ErrPermission }

// TestDecodeRejectsMissingFile 校验显式解码时文件不存在会明确报错.
//
// 静默跳过会让用户看到 "没有找到二维码", 误以为图片里没有码.
func TestDecodeRejectsMissingFile(t *testing.T) {
	env := fakeEnv(nil, nil, "", nil, true)

	code, out, errs := runWith(t, []string{"d", "missing.png"}, env)
	if code != exitUsage {
		t.Fatalf("退出码 = %d, want %d", code, exitUsage)
	}
	if !bytes.Contains([]byte(errs), []byte("不存在")) {
		t.Fatalf("错误信息应说明文件不存在: %s", errs)
	}
	if out != "" {
		t.Fatalf("失败时不应写标准输出: %q", out)
	}
}

// TestDecodeRejectsTextFile 校验显式解码不接受文本文件.
func TestDecodeRejectsTextFile(t *testing.T) {
	files := map[string][]byte{"note.txt": []byte("plain text")}
	env := fakeEnv(files, nil, "", nil, true)

	code, _, errs := runWith(t, []string{"d", "note.txt"}, env)
	if code != exitUsage {
		t.Fatalf("退出码 = %d, want %d", code, exitUsage)
	}
	if !bytes.Contains([]byte(errs), []byte("不是可识别的图片")) {
		t.Fatalf("错误信息应说明不是图片: %s", errs)
	}
}

// TestDecodeRejectsClipboardText 校验剪贴板只有文本时的提示.
func TestDecodeRejectsClipboardText(t *testing.T) {
	env := fakeEnv(nil, nil, "clipboard text", nil, true)

	code, _, errs := runWith(t, []string{"d", "-i", "clipboard"}, env)
	if code != exitUsage {
		t.Fatalf("退出码 = %d, want %d", code, exitUsage)
	}
	if !bytes.Contains([]byte(errs), []byte("剪贴板中没有图片")) {
		t.Fatalf("错误信息应说明剪贴板没有图片: %s", errs)
	}
}

// TestEncodeTrimsPipeNewline 校验管道文本的尾随换行不会进入二维码内容.
func TestEncodeTrimsPipeNewline(t *testing.T) {
	env := fakeEnv(nil, nil, "", []byte("piped-value\n"), false)

	code, out, errs := runWith(t, nil, env)
	if code != exitOK {
		t.Fatalf("退出码 = %d (stderr: %s)", code, errs)
	}

	// 输出是 PNG, 用解码器读回来确认内容.
	decoded := decodePNG(t, []byte(out))
	if decoded != "piped-value" {
		t.Fatalf("内容 = %q, want %q", decoded, "piped-value")
	}
}

// TestLogReportsSourceAndMode 校验每种输入来源与模式都会输出一行日志.
//
// 自动模式会替用户决定解码还是编码, 输入也可能来自剪贴板或管道,
// 缺少这行日志用户就不知道命令刚才读了什么, 做了什么.
func TestLogReportsSourceAndMode(t *testing.T) {
	png := qrPNG(t, "log-payload")

	cases := []struct {
		name       string
		args       []string
		env        source.Env
		wantMode   string
		wantBy     string
		wantOrigin string
		wantOutput string
	}{
		{
			name:       "管道图片自动解码",
			args:       nil,
			env:        fakeEnv(nil, nil, "", png, false),
			wantMode:   "decode",
			wantBy:     "auto",
			wantOrigin: "stdin",
			wantOutput: "output=stdout",
		},
		{
			name:       "管道文本自动编码",
			args:       nil,
			env:        fakeEnv(nil, nil, "", []byte("text"), false),
			wantMode:   "encode",
			wantBy:     "auto",
			wantOrigin: "stdin",
			wantOutput: "output=stdout(png)",
		},
		{
			name:       "剪贴板图片自动解码",
			args:       nil,
			env:        fakeEnv(nil, png, "", nil, true),
			wantMode:   "decode",
			wantBy:     "auto",
			wantOrigin: "clipboard",
			wantOutput: "output=stdout",
		},
		{
			name:       "显式解码 JSON",
			args:       []string{"d", "shot.png", "--json"},
			env:        fakeEnv(map[string][]byte{"shot.png": png}, nil, "", nil, true),
			wantMode:   "decode",
			wantBy:     "explicit",
			wantOrigin: "file",
			wantOutput: "output=stdout(json)",
		},
		{
			name:       "显式编码到文件",
			args:       []string{"e", "hello", "-o", "out.png"},
			env:        fakeEnv(nil, nil, "", nil, true),
			wantMode:   "encode",
			wantBy:     "explicit",
			wantOrigin: "arg",
			wantOutput: "output=file:out.png",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 编码到文件的用例会真的写盘, 结束后清理.
			if c.wantOutput == "output=file:out.png" {
				t.Cleanup(func() { _ = os.Remove("out.png") })
			}

			_, _, _, logs := runWithLog(t, c.args, c.env)
			for _, want := range []string{
				"mode=" + c.wantMode,
				"by=" + c.wantBy,
				"origin=" + c.wantOrigin,
				c.wantOutput,
			} {
				if !bytes.Contains([]byte(logs), []byte(want)) {
					t.Fatalf("日志缺少 %q:\n%s", want, logs)
				}
			}
		})
	}
}

// TestLogGoesToStderrNotStdout 校验日志不会污染 stdout 上的结果.
//
// stdout 可能是二维码内容或 PNG 二进制, 混入日志会破坏管道.
func TestLogGoesToStderrNotStdout(t *testing.T) {
	env := fakeEnv(nil, nil, "", []byte("plain"), false)

	_, out, _, logs := runWithLog(t, nil, env)
	if logs == "" {
		t.Fatal("应当产生日志")
	}
	if bytes.Contains([]byte(out), []byte("输入源与模式")) {
		t.Fatalf("日志不应出现在标准输出: %q", out)
	}
	// 标准输出应当是纯 PNG.
	if format, ok := imageio.SniffFormat([]byte(out)); !ok || format != imageio.FormatPNG {
		t.Fatalf("标准输出不是 PNG, 格式 = %v", format)
	}
}

// TestLogCoversEveryInputOfMultiFile 校验多文件解码时每个来源各记一行.
func TestLogCoversEveryInputOfMultiFile(t *testing.T) {
	files := map[string][]byte{
		"one.png": qrPNG(t, "first"),
		"two.png": qrPNG(t, "second"),
	}
	env := fakeEnv(files, nil, "", nil, true)

	_, _, _, logs := runWithLog(t, []string{"d", "one.png", "two.png"}, env)
	if got := bytes.Count([]byte(logs), []byte("输入源与模式")); got != 2 {
		t.Fatalf("日志行数 = %d, want 2:\n%s", got, logs)
	}
	for _, name := range []string{"one.png", "two.png"} {
		if !bytes.Contains([]byte(logs), []byte(name)) {
			t.Fatalf("日志缺少来源 %q:\n%s", name, logs)
		}
	}
}

// TestLogReportsSkippedInput 校验被跳过的输入也会留下日志.
//
// 解码模式拿到的文本无法处理, 如果不记录, 用户会疑惑自己给的输入
// 为什么没有产生结果.
func TestLogReportsSkippedInput(t *testing.T) {
	var out, errb, logb bytes.Buffer
	r := &Runner{
		Stdout:    &out,
		Stderr:    &errb,
		LogOutput: &logb,
	}
	inputs := []source.Input{
		{Origin: source.OriginFile, Label: "text.txt", Kind: source.KindText, Data: []byte("plain")},
	}

	// 直接驱动解码流程时需要先初始化日志, 否则会落到全局 logger.
	r.setupLogging(false)

	code := r.runDecode(context.Background(), byExplicit, decodeOptions{},
		[]source.Input{inputs[0]})
	if code != exitNoCode {
		t.Fatalf("退出码 = %d, want %d", code, exitNoCode)
	}
	if !bytes.Contains(logb.Bytes(), []byte("跳过非图片输入")) {
		t.Fatalf("日志缺少跳过提示:\n%s", logb.String())
	}
	if !bytes.Contains(logb.Bytes(), []byte("text.txt")) {
		t.Fatalf("日志缺少来源名称:\n%s", logb.String())
	}
}

// TestLogShowsInputContent 校验日志里直接显示输入内容, 而不只是来源名称.
//
// 只知道 "参数" 或 "剪贴板" 无法确认读到的到底是哪份数据.
func TestLogShowsInputContent(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		env         source.Env
		wantContent string
	}{
		{
			name:        "参数文本",
			args:        []string{"e", "hello-world"},
			env:         fakeEnv(nil, nil, "", nil, true),
			wantContent: "content=hello-world",
		},
		{
			name:        "剪贴板图片显示格式",
			args:        nil,
			env:         fakeEnv(nil, qrPNG(t, "clip"), "", nil, true),
			wantContent: "content=png",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, logs := runWithLog(t, c.args, c.env)
			if !bytes.Contains([]byte(logs), []byte(c.wantContent)) {
				t.Fatalf("日志缺少 %q:\n%s", c.wantContent, logs)
			}
		})
	}
}

// TestLogEscapesNewlines 校验多行文本不会把日志撑成多行.
func TestLogEscapesNewlines(t *testing.T) {
	env := fakeEnv(nil, nil, "", []byte("line1\nline2"), false)

	_, _, _, logs := runWithLog(t, []string{"e", "-i", "-"}, env)
	if bytes.Contains([]byte(logs), []byte("line1\nline2")) {
		t.Fatalf("日志里不应出现真实换行:\n%s", logs)
	}
	if !bytes.Contains([]byte(logs), []byte(`line1\nline2`)) {
		t.Fatalf("换行应转义显示:\n%s", logs)
	}
}

// TestLogTruncatesLongContent 校验超长输入被截断并说明总长度.
func TestLogTruncatesLongContent(t *testing.T) {
	long := strings.Repeat("a", 500)
	env := fakeEnv(nil, nil, "", []byte(long), false)

	_, _, _, logs := runWithLog(t, []string{"e", "-i", "-"}, env)
	if !bytes.Contains([]byte(logs), []byte("共 500 字符")) {
		t.Fatalf("日志应说明原始长度:\n%s", logs)
	}
	if bytes.Count([]byte(logs), []byte("a")) > 300 {
		t.Fatalf("日志里的内容未被截断")
	}
}

// TestWebOpensBrowser 校验网页模式会请求打开浏览器, 并记录真实地址.
func TestWebOpensBrowser(t *testing.T) {
	var opened []string
	code, _, errs, logs := runWebCase(t, []string{"e", "payload", "--web"}, &opened)
	if code != exitOK {
		t.Fatalf("退出码 = %d (stderr: %s)", code, errs)
	}
	if len(opened) != 1 {
		t.Fatalf("打开次数 = %d, want 1 (%v)", len(opened), opened)
	}
	if !strings.HasPrefix(opened[0], "http://127.0.0.1:") {
		t.Fatalf("打开的地址 = %q", opened[0])
	}
	if !bytes.Contains([]byte(logs), []byte("已用默认浏览器打开")) {
		t.Fatalf("日志应说明已打开浏览器:\n%s", logs)
	}
}

// TestWebNoOpenSkipsBrowser 校验 --no-open 不打开浏览器.
func TestWebNoOpenSkipsBrowser(t *testing.T) {
	var opened []string
	code, _, _, logs := runWebCase(t, []string{"e", "payload", "--web", "--no-open"}, &opened)
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if len(opened) != 0 {
		t.Fatalf("--no-open 时不应打开浏览器, 实际: %v", opened)
	}
	if !bytes.Contains([]byte(logs), []byte("已跳过自动打开浏览器")) {
		t.Fatalf("日志应说明跳过了打开:\n%s", logs)
	}
}

// TestWebOpenFailureKeepsServing 校验打开浏览器失败不影响服务退出码.
func TestWebOpenFailureKeepsServing(t *testing.T) {
	tty := true
	var out, errb, logb bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	r := &Runner{
		Stdout:           &out,
		Stderr:           &errb,
		LogOutput:        &logb,
		StdinIsTerminal:  true,
		StdoutIsTerminal: &tty,
		OpenURL:          func(string) error { return errors.New("模拟失败") },
	}
	code := r.Run(ctx, []string{"e", "x", "--web"})
	if code != exitOK {
		t.Fatalf("打开失败不应改变退出码, 实际 %d (stderr: %s)", code, errb.String())
	}
	if !bytes.Contains(logb.Bytes(), []byte("自动打开浏览器失败")) {
		t.Fatalf("日志应记录失败:\n%s", logb.String())
	}
}

// runWebCase 在受控环境中跑一次会启动网页服务的命令.
func runWebCase(t *testing.T, args []string, opened *[]string) (int, string, string, string) {
	t.Helper()

	tty := true
	var out, errb, logb bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	r := &Runner{
		Stdout:           &out,
		Stderr:           &errb,
		LogOutput:        &logb,
		StdinIsTerminal:  true,
		StdoutIsTerminal: &tty,
		OpenURL: func(u string) error {
			*opened = append(*opened, u)
			return nil
		},
	}
	code := r.Run(ctx, args)
	return code, out.String(), errb.String(), logb.String()
}

// TestPrintCamHits 校验摄像头扫码结果的输出与退出码.
func TestPrintCamHits(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &Runner{Stdout: &stdout, Stderr: &stderr}

	if code := runner.printCamHits([]string{"first", "second"}); code != exitOK {
		t.Fatalf("退出码 = %d, want %d", code, exitOK)
	}
	if got := stdout.String(); got != "first\nsecond\n" {
		t.Fatalf("stdout = %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runner.printCamHits(nil); code != exitNoCode {
		t.Fatalf("空结果的退出码 = %d, want %d", code, exitNoCode)
	}
	if !strings.Contains(stderr.String(), "没有找到二维码") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
