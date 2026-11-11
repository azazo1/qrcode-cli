// Package imageio 提供图片解码与格式识别.
//
// 自动模式需要在不完整解码图片的前提下判断输入到底是不是图片, 因此这里
// 除了常规的 image.Decode 之外, 还提供一份独立的魔数表.
package imageio

import (
	"bytes"
	"fmt"
	"image"
	"io"

	// 注册标准库之外的图片格式解码器.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	// 标准库格式.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Format 表示被支持的图片格式的规范名.
type Format string

const (
	FormatPNG  Format = "png"
	FormatJPEG Format = "jpeg"
	FormatGIF  Format = "gif"
	FormatBMP  Format = "bmp"
	FormatTIFF Format = "tiff"
	FormatWEBP Format = "webp"
)

// magicPrefix 描述一种图片格式的文件头特征.
// 有些格式 (如 RIFF 包裹的 webp) 不能只看开头几个字节, 因此额外带一个偏移检查.
type magicPrefix struct {
	format Format
	prefix []byte
	// offset 与 extra 用于需要二次校验的格式; extra 为空表示不校验.
	offset int
	extra  []byte
}

var magicTable = []magicPrefix{
	{format: FormatPNG, prefix: []byte("\x89PNG\r\n\x1a\n")},
	{format: FormatJPEG, prefix: []byte("\xff\xd8\xff")},
	{format: FormatGIF, prefix: []byte("GIF87a")},
	{format: FormatGIF, prefix: []byte("GIF89a")},
	{format: FormatBMP, prefix: []byte("BM")},
	{format: FormatTIFF, prefix: []byte("II\x2a\x00")},
	{format: FormatTIFF, prefix: []byte("MM\x00\x2a")},
	// RIFF....WEBP: 前 4 字节 RIFF, 第 8 字节起是 WEBP.
	{format: FormatWEBP, prefix: []byte("RIFF"), offset: 8, extra: []byte("WEBP")},
}

// maxMagicLen 是所有魔数规则中需要预读的最大字节数.
func maxMagicLen() int {
	largest := 0
	for _, m := range magicTable {
		need := len(m.prefix)
		if m.offset+len(m.extra) > need {
			need = m.offset + len(m.extra)
		}
		if need > largest {
			largest = need
		}
	}
	return largest
}

// SniffFormat 根据文件头判断图片格式.
// 只在读到的字节足够时给出结论, 截断或非图片输入返回 ok=false.
func SniffFormat(head []byte) (Format, bool) {
	if len(head) > maxMagicLen() {
		head = head[:maxMagicLen()]
	}
	for _, m := range magicTable {
		if len(head) < len(m.prefix) || !bytes.HasPrefix(head, m.prefix) {
			continue
		}
		if len(m.extra) > 0 {
			if len(head) < m.offset+len(m.extra) {
				continue
			}
			if !bytes.Equal(head[m.offset:m.offset+len(m.extra)], m.extra) {
				continue
			}
		}
		return m.format, true
	}
	return "", false
}

// SniffBytes 是 SniffFormat 针对完整字节切片的便捷封装.
func SniffBytes(data []byte) (Format, bool) {
	return SniffFormat(data)
}

// Decode 解码图片并返回图像与其规范名格式.
// 调用方传入的 data 必须包含完整图片内容.
func Decode(data []byte) (image.Image, Format, error) {
	format, ok := SniffFormat(data)
	if !ok {
		return nil, "", fmt.Errorf("不是可识别的图片格式")
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, format, fmt.Errorf("解码 %s 图片失败: %w", format, err)
	}
	return img, format, nil
}

// DecodeFrom 从 reader 读取全部内容后解码.
func DecodeFrom(r io.Reader) (image.Image, Format, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("读取图片数据失败: %w", err)
	}
	return Decode(data)
}
