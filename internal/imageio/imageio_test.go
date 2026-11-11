package imageio

import "testing"

// TestSniffFormat 覆盖各格式的魔数判定, 以及非图片与截断输入.
func TestSniffFormat(t *testing.T) {
	cases := []struct {
		name   string
		head   []byte
		want   Format
		wantOK bool
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n"), FormatPNG, true},
		{"jpeg", []byte("\xff\xd8\xff\xe0"), FormatJPEG, true},
		{"gif87", []byte("GIF87a"), FormatGIF, true},
		{"gif89", []byte("GIF89a"), FormatGIF, true},
		{"bmp", []byte("BM\x00\x00"), FormatBMP, true},
		{"tiff-le", []byte("II\x2a\x00"), FormatTIFF, true},
		{"tiff-be", []byte("MM\x00\x2a"), FormatTIFF, true},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), FormatWEBP, true},
		// RIFF 但子类型不是 WEBP, 不能误判成图片.
		{"riff-wave", []byte("RIFF\x00\x00\x00\x00WAVEfmt "), "", false},
		{"plain-text", []byte("hello world"), "", false},
		{"empty", []byte{}, "", false},
		// 只有 PNG 前 4 字节, 不足以确认.
		{"truncated-png", []byte("\x89PNG"), "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := SniffFormat(c.head)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if got != c.want {
				t.Fatalf("format = %q, want %q", got, c.want)
			}
		})
	}
}
