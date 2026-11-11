// Package terminal 负责在终端里以半块字符高密度显示二维码.
//
// 一个终端字符格子的宽高比大约是 1:2, 因此把竖直方向的两个模块合并到一个
// 字符里, 正好得到接近正方形的二维码, 同时把所需行数减半.
package terminal

import (
	"strings"

	"github.com/azazo1/qrcode-cli/internal/encode"
)

// 用于绘制的半块字符.
const (
	// blockFull 上下两个模块都是黑色.
	blockFull = '█'
	// blockUpper 只有上方模块是黑色.
	blockUpper = '▀'
	// blockLower 只有下方模块是黑色.
	blockLower = '▄'
	// blockEmpty 两个模块都是白色.
	blockEmpty = ' '
)

// ANSI 转义序列. 使用黑底白前景的显式配色, 保证在浅色与深色主题下都有对比度.
const (
	ansiPrefix = "\x1b[30;47m"
	ansiReset  = "\x1b[0m"
)

// Dimensions 返回在终端显示该矩阵时占用的字符宽高.
//
// 宽度等于模块数, 高度为模块数的一半向上取整.
func Dimensions(m *encode.Matrix) (cols, rows int) {
	side := m.Size()
	return side, (side + 1) / 2
}

// Fits 判断矩阵能否放进给定尺寸的终端.
//
// 留出两列两行的余量, 避免二维码贴边或被终端提示符回卷打断.
func Fits(m *encode.Matrix, termCols, termRows int) bool {
	cols, rows := Dimensions(m)
	return cols+2 <= termCols && rows+2 <= termRows
}

// Render 把矩阵渲染成可供终端输出的字符串.
// color 为 false 时只输出字符, 不带 ANSI 转义序列.
func Render(m *encode.Matrix, color bool) string {
	var b strings.Builder

	side := m.Size()
	_, rows := Dimensions(m)
	// 每行字符都带颜色前缀, 避免终端在行内出现颜色断裂.
	linePrefix := ""
	lineSuffix := ""
	if color {
		linePrefix = ansiPrefix
		lineSuffix = ansiReset
	}

	for row := 0; row < rows; row++ {
		b.WriteString(linePrefix)
		upper := row * 2
		lower := upper + 1

		for col := 0; col < side; col++ {
			top := m.At(upper, col)
			// 模块数为奇数时最后一行没有下半部分, 视为白色.
			bottom := false
			if lower < side {
				bottom = m.At(lower, col)
			}
			b.WriteRune(cell(top, bottom))
		}
		b.WriteString(lineSuffix)
		b.WriteByte('\n')
	}

	return b.String()
}

// cell 根据上下两个模块的颜色选择字符.
func cell(top, bottom bool) rune {
	switch {
	case top && bottom:
		return blockFull
	case top:
		return blockUpper
	case bottom:
		return blockLower
	default:
		return blockEmpty
	}
}
