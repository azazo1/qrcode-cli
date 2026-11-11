// Package encode 负责把文本编码成二维码矩阵与 PNG 图片.
package encode

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// Level 是二维码的纠错等级.
type Level string

const (
	// LevelLow 约可恢复 7% 的损坏.
	LevelLow Level = "L"
	// LevelMedium 约可恢复 15% 的损坏.
	LevelMedium Level = "M"
	// LevelQuartile 约可恢复 25% 的损坏.
	LevelQuartile Level = "Q"
	// LevelHigh 约可恢复 30% 的损坏.
	LevelHigh Level = "H"
)

// ParseLevel 解析命令行传入的纠错等级, 大小写不敏感.
func ParseLevel(s string) (Level, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "L":
		return LevelLow, nil
	case "M", "":
		return LevelMedium, nil
	case "Q":
		return LevelQuartile, nil
	case "H":
		return LevelHigh, nil
	default:
		return "", fmt.Errorf("无效的纠错等级 %q, 可选值为 L, M, Q, H", s)
	}
}

// recoveryLevel 把内部等级映射到编码库的枚举.
//
// 编码库的命名与标准略有偏差: 它的 High 对应标准 Q (25%), Highest 对应标准 H (30%).
func (l Level) recoveryLevel() qrcode.RecoveryLevel {
	switch l {
	case LevelLow:
		return qrcode.Low
	case LevelQuartile:
		return qrcode.High
	case LevelHigh:
		return qrcode.Highest
	default:
		return qrcode.Medium
	}
}

// Matrix 是二维码的模块矩阵, true 表示黑色模块.
//
// 矩阵已经包含 4 个模块宽的静区, 与标准要求的空白边距一致,
// 因此渲染时不需要再额外补边.
type Matrix struct {
	modules [][]bool
	// Version 是二维码的版本号 (1..40).
	Version int
	// Level 是本次编码使用的纠错等级.
	Level Level
	// Content 是被编码的原始文本.
	Content string
}

// Size 返回矩阵的边长 (模块数).
func (m *Matrix) Size() int {
	return len(m.modules)
}

// At 返回指定行列的模块是否为黑色.
func (m *Matrix) At(row, col int) bool {
	if row < 0 || row >= len(m.modules) {
		return false
	}
	if col < 0 || col >= len(m.modules[row]) {
		return false
	}
	return m.modules[row][col]
}

// Build 把文本编码成二维码矩阵.
func Build(content string, level Level) (*Matrix, error) {
	if content == "" {
		return nil, fmt.Errorf("待编码内容不能为空")
	}

	code, err := qrcode.New(content, level.recoveryLevel())
	if err != nil {
		// 库原样返回英文错误, 这里换成能指导操作的中文说明.
		if strings.Contains(err.Error(), "too long") {
			return nil, fmt.Errorf(
				"内容过长, 纠错等级 %s 最多容纳约 %d 字节, 当前为 %d 字节; 可降低纠错等级 (例如 -l L) 或缩短内容",
				level, maxCapacity(level), len(content))
		}
		return nil, fmt.Errorf("编码二维码失败: %w", err)
	}

	// Bitmap 返回的矩阵自带静区.
	raw := code.Bitmap()
	return &Matrix{
		modules: raw,
		Version: code.VersionNumber,
		Level:   level,
		Content: content,
	}, nil
}

// maxCapacity 返回各纠错等级下字节模式的理论最大容量.
//
// 数值取自 QR 标准的版本 40 表格: 字节模式可容纳的字节数,
// 用于在内容超长时给出可比较的参考值.
func maxCapacity(level Level) int {
	switch level {
	case LevelLow:
		return 2953
	case LevelQuartile:
		return 1663
	case LevelHigh:
		return 1273
	default:
		return 2331
	}
}

// RenderPNG 把矩阵渲染成 PNG 图片, 每个模块占 size 个像素.
func (m *Matrix) RenderPNG(size int) ([]byte, error) {
	if size < 1 || size > 64 {
		return nil, fmt.Errorf("模块像素大小 %d 超出范围 1..64", size)
	}

	side := m.Size()
	img := image.NewRGBA(image.Rect(0, 0, side*size, side*size))

	black := color.RGBA{A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	for row := 0; row < side; row++ {
		for col := 0; col < side; col++ {
			c := white
			if m.At(row, col) {
				c = black
			}
			for dy := 0; dy < size; dy++ {
				y := row*size + dy
				for dx := 0; dx < size; dx++ {
					img.SetRGBA(col*size+dx, y, c)
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("编码 PNG 失败: %w", err)
	}
	return buf.Bytes(), nil
}
