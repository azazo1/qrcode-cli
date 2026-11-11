package cli

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"testing"

	"github.com/azazo1/qrcode-cli/internal/decode"
	"github.com/azazo1/qrcode-cli/internal/encode"
	"github.com/azazo1/qrcode-cli/internal/imageio"
)

// blankPNG 生成一张不含任何二维码的纯白 PNG, 用于验证"未找到二维码"分支.
func blankPNG() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// trimNewline 去掉输出末尾的换行, 便于断言单条内容.
func trimNewline(s string) string {
	return strings.TrimSuffix(s, "\n")
}

// buildMatrixFor 构造指定内容对应的矩阵, 便于断言尺寸相关行为.
func buildMatrixFor(t *testing.T, content string) *encode.Matrix {
	t.Helper()
	m, err := encode.Build(content, encode.LevelMedium)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

// decodePNG 解码 PNG 字节里的唯一二维码, 返回其内容.
func decodePNG(t *testing.T, data []byte) string {
	t.Helper()

	img, _, err := imageio.Decode(data)
	if err != nil {
		t.Fatalf("解码 PNG: %v", err)
	}
	results, err := decode.New(decode.Options{}).Decode(context.Background(), img)
	if err != nil {
		t.Fatalf("识别二维码: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("结果数 = %d, want 1", len(results))
	}
	return results[0].Text
}
