package source

import (
	"bytes"
	"os"
	"testing"
	"time"
)

// TestTrimTrailingNewline 覆盖尾部换行的裁剪规则.
func TestTrimTrailingNewline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"无换行", "abc", "abc"},
		{"单个换行", "abc\n", "abc"},
		{"CRLF", "abc\r\n", "abc"},
		{"两个换行只去一个", "abc\n\n", "abc\n"},
		{"只有换行时保留原样", "\n", "\n"},
		{"空输入", "", ""},
		{"行内换行不受影响", "a\nb\n", "a\nb"},
		{"尾部空格不算换行", "abc \n", "abc "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(TrimTrailingNewline([]byte(c.in))); got != c.want {
				t.Fatalf("TrimTrailingNewline(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestFromStdinTrimsTextNewline 校验管道文本会去掉尾随换行.
func TestFromStdinTrimsTextNewline(t *testing.T) {
	env := Env{Stdin: bytes.NewReader([]byte("hello\n"))}
	input, err := env.FromStdin()
	if err != nil {
		t.Fatalf("FromStdin: %v", err)
	}
	if string(input.Data) != "hello" {
		t.Fatalf("内容 = %q, want %q", string(input.Data), "hello")
	}
}

// TestFromStdinKeepsImageBytes 校验图片输入不会被当作文本处理.
func TestFromStdinKeepsImageBytes(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + "binary-body\n")
	env := Env{Stdin: bytes.NewReader(png)}
	input, err := env.FromStdin()
	if err != nil {
		t.Fatalf("FromStdin: %v", err)
	}
	if input.Kind != KindImage {
		t.Fatalf("kind = %v, want %v", input.Kind, KindImage)
	}
	if !bytes.Equal(input.Data, png) {
		t.Fatal("图片数据不应被裁剪")
	}
}

// TestFromArgsKeepsExplicitNewline 校验显式参数保持原样, 这是编码换行的出口.
func TestFromArgsKeepsExplicitNewline(t *testing.T) {
	env := notAFileEnv()

	input, err := env.FromArgs([]string{"a\nb"})
	if err != nil {
		t.Fatalf("FromArgs: %v", err)
	}
	if string(input.Data) != "a\nb" {
		t.Fatalf("内容 = %q, want %q", string(input.Data), "a\nb")
	}
}

// TestFromArgsFileTrimsNewline 校验文本文件末尾的换行会被去掉.
func TestFromArgsFileTrimsNewline(t *testing.T) {
	env := Env{
		ReadFile: func(string) ([]byte, error) { return []byte("file-body\n"), nil },
		Stat: func(string) (os.FileInfo, error) {
			return fakeFileInfo{name: "a.txt"}, nil
		},
	}

	input, err := env.FromArgs([]string{"a.txt"})
	if err != nil {
		t.Fatalf("FromArgs: %v", err)
	}
	if string(input.Data) != "file-body" {
		t.Fatalf("内容 = %q, want %q", string(input.Data), "file-body")
	}
}

// TestClassify 校验按魔数区分图片与文本.
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want Kind
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n"), KindImage},
		{"jpeg", []byte("\xff\xd8\xff\xe0"), KindImage},
		{"plain", []byte("hello"), KindText},
		{"empty", nil, KindText},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := Classify(c.data); got != c.want {
				t.Fatalf("Classify = %v, want %v", got, c.want)
			}
		})
	}
}

// notAFileEnv 返回一个所有路径都不存在的环境, 让 FromArgs 按文本处理.
func notAFileEnv() Env {
	return Env{
		Stat: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	}
}

// fakeFileInfo 是最小的 os.FileInfo 实现, 用于让 FromArgs 认为路径是普通文件.
type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string { return f.name }
func (f fakeFileInfo) Size() int64  { return 0 }
func (f fakeFileInfo) Mode() os.FileMode {
	return 0o644
}
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }
