package cli

import (
	"bytes"
	"context"
	"os"
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

	var out, errb bytes.Buffer
	r := &Runner{
		Stdout:          &out,
		Stderr:          &errb,
		StdinIsTerminal: env.StdinIsTerminal,
		Env:             &env,
	}
	code := r.Run(context.Background(), args)
	return code, out.String(), errb.String()
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
