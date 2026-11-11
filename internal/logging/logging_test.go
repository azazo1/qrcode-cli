package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestContentIsGrayWhenColorEnabled 校验开启颜色时 content 的值被浅灰色包裹.
func TestContentIsGrayWhenColorEnabled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(&buf, slog.LevelInfo, true))

	logger.Info("输入源与模式", "mode", "encode", "content", "hello world")

	out := buf.String()
	if !strings.Contains(out, "content="+ansiGray+"\"hello world\""+ansiReset) {
		t.Fatalf("content 未被灰色包裹:\n%q", out)
	}
}

// TestOnlyContentIsColored 校验其他字段不加颜色, 否则整行花花绿绿反而难读.
func TestOnlyContentIsColored(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(&buf, slog.LevelInfo, true))

	logger.Info("测试", "mode", "encode", "content", "abc")

	out := buf.String()
	if strings.Count(out, ansiGray) != 1 {
		t.Fatalf("颜色序列应只出现一次:\n%q", out)
	}
	if !strings.Contains(out, "mode=encode") {
		t.Fatalf("mode 字段被污染:\n%q", out)
	}
}

// TestNoColorWhenDisabled 校验关闭颜色时不出现任何转义序列.
//
// 输出被重定向到文件或管道时必须如此, 否则文件里会混进控制字符.
func TestNoColorWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(&buf, slog.LevelInfo, false))

	logger.Info("测试", "content", "abc")

	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Fatalf("不应出现 ANSI 转义序列:\n%q", out)
	}
	if !strings.Contains(out, "content=abc") {
		t.Fatalf("content 未正常输出:\n%q", out)
	}
}

// TestBufferIsNotColored 校验非文件目标不会被判定为终端.
//
// 测试与管道都往缓冲区写, 这类目标不能加颜色.
func TestBufferIsNotColored(t *testing.T) {
	var buf bytes.Buffer
	if colorEnabled(&buf) {
		t.Fatal("bytes.Buffer 不应被判定为终端")
	}
}

// TestNoColorEnvDisablesColor 校验 NO_COLOR 约定被遵守.
func TestNoColorEnvDisablesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf bytes.Buffer
	if colorEnabled(&buf) {
		t.Fatal("NO_COLOR 非空时应关闭颜色")
	}
}

// TestDumbTerminalDisablesColor 校验 TERM=dumb 时关闭颜色.
func TestDumbTerminalDisablesColor(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("NO_COLOR", "")

	var buf bytes.Buffer
	if colorEnabled(&buf) {
		t.Fatal("TERM=dumb 时应关闭颜色")
	}
}

// TestQuotingKeepsFieldsSeparable 校验含空格或换行的值会被加引号.
//
// 不加引号时 "content=hello world bytes=9" 无法区分字段边界.
func TestQuotingKeepsFieldsSeparable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"无特殊字符", "plain", "plain"},
		{"含空格", "hello world", `"hello world"`},
		{"含换行", "a\nb", `"a\nb"`},
		{"含等号", "a=b", `"a=b"`},
		{"空值", "", `""`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quoteIfNeeded(c.in); got != c.want {
				t.Fatalf("quoteIfNeeded(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestHandleRespectsLevel 校验低于阈值的记录被丢弃.
func TestHandleRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(&buf, slog.LevelWarn, false)

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Info 级别应被过滤")
	}
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("Warn 级别应被放行")
	}
}

// TestWithAttrsAndGroups 校验 With/handler 组合不会丢失字段.
func TestWithAttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(&buf, slog.LevelInfo, false)).
		With("mode", "decode").
		WithGroup("input")

	logger.Info("测试", "source", "a.png")

	out := buf.String()
	for _, want := range []string{"mode=decode", "input.source=a.png"} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q:\n%q", want, out)
		}
	}
}

// TestTimeAndLevelFormat 校验时间只保留时分秒, 级别名小写.
func TestTimeAndLevelFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(&buf, slog.LevelInfo, false))

	logger.Warn("测试")

	out := buf.String()
	if strings.Contains(out, "T") && strings.Contains(out, "+08:00") {
		t.Fatalf("时间不应带完整日期与时区:\n%q", out)
	}
	if !strings.Contains(out, "level=warn") {
		t.Fatalf("级别名应为小写:\n%q", out)
	}
}
