package imageio

import (
	"image"
	"image/draw"
)

// Enhance 生成用于困难场景的增强灰度图.
//
// 处理顺序: 转灰度 -> 对比度拉伸 -> 3x3 中值滤波.
//
//   - 灰度化后二值化只看亮度, 避免彩色噪声干扰.
//   - 拉伸对比度让低对比度图片 (浅灰底, 深灰码) 恢复到全量程.
//   - 中值滤波去掉椒盐噪声和压缩伪影留下的孤立像素, 同时保留二维码的方块边缘,
//     这是均值模糊做不到的.
//
// 该变体代价明显高于原图, 只在常规变体识别失败时使用.
func Enhance(img image.Image) *image.Gray {
	gray := ToGray(img)
	stretched := StretchContrast(gray)
	return Median3(stretched)
}

// ToGray 转换成灰度图, 使用标准的亮度加权系数.
//
// 借助 image/draw 的 Gray 目标模型完成转换, 比逐像素调用 At 快很多,
// 也能正确处理带调色板的 PNG 与 JPEG 的 YCbCr 数据.
func ToGray(img image.Image) *image.Gray {
	b := img.Bounds()
	dst := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// stretchLowPercentile 与 stretchHighPercentile 定义拉伸时使用的分位点.
//
// 用分位点而不是最小最大值, 避免个别异常像素把有效范围压扁.
const (
	stretchLowPercentile  = 2
	stretchHighPercentile = 98
	minStretchSpan        = 8
)

// StretchContrast 把亮度线性拉伸到全量程.
//
// 对已经很均匀的图片 (黑白二维码) 几乎没有影响: 分位点本身就落在两端.
// 跨度太小时跳过拉伸, 避免把噪声放大成伪信号.
func StretchContrast(src *image.Gray) *image.Gray {
	if src == nil || len(src.Pix) == 0 {
		return src
	}

	var histogram [256]int
	total := 0
	for y := 0; y < src.Rect.Dy(); y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+src.Rect.Dx()]
		for _, v := range row {
			histogram[v]++
			total++
		}
	}
	if total == 0 {
		return src
	}

	low := percentile(histogram[:], total, stretchLowPercentile)
	high := percentile(histogram[:], total, stretchHighPercentile)
	if high-low < minStretchSpan {
		return src
	}

	dst := image.NewGray(src.Rect)
	span := int(high) - int(low)
	for y := 0; y < src.Rect.Dy(); y++ {
		srcRow := src.Pix[y*src.Stride : y*src.Stride+src.Rect.Dx()]
		dstRow := dst.Pix[y*dst.Stride : y*dst.Stride+dst.Rect.Dx()]
		for x, v := range srcRow {
			shifted := (int(v) - int(low)) * 255 / span
			switch {
			case shifted < 0:
				dstRow[x] = 0
			case shifted > 255:
				dstRow[x] = 255
			default:
				dstRow[x] = uint8(shifted)
			}
		}
	}
	return dst
}

// percentile 返回直方图里累计占比达到 pct 的最小亮度值.
//
// 目标计数至少取 1: 像素数很少时整数除法会把 target 算成 0, 那样循环第一步
// 就会命中并返回 0, 把一个均匀的图误判成满量程.
func percentile(histogram []int, total, pct int) uint8 {
	target := total * pct / 100
	if target < 1 {
		target = 1
	}

	accumulated := 0
	for value, count := range histogram {
		accumulated += count
		if accumulated >= target {
			return uint8(value)
		}
	}
	return uint8(len(histogram) - 1)
}

// Median3 对灰度图做 3x3 中值滤波.
//
// 中值滤波能去掉孤立噪点, 同时保住二维码的方正边缘, 因此比均值模糊更适合
// 二值化的前置处理. 边缘像素沿用原值, 不做边界外推.
func Median3(src *image.Gray) *image.Gray {
	if src == nil || src.Rect.Dx() < 3 || src.Rect.Dy() < 3 {
		return src
	}

	width, height := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewGray(src.Rect)
	copy(dst.Pix, src.Pix)

	var window [9]uint8
	for y := 1; y < height-1; y++ {
		up := src.Pix[(y-1)*src.Stride:]
		mid := src.Pix[y*src.Stride:]
		down := src.Pix[(y+1)*src.Stride:]
		out := dst.Pix[y*dst.Stride:]

		for x := 1; x < width-1; x++ {
			window[0], window[1], window[2] = up[x-1], up[x], up[x+1]
			window[3], window[4], window[5] = mid[x-1], mid[x], mid[x+1]
			window[6], window[7], window[8] = down[x-1], down[x], down[x+1]
			out[x] = median9(&window)
		}
	}
	return dst
}

// median9 用插入排序取 9 个元素的中值.
//
// 元素只有 9 个, 插入排序的比较次数很少, 且实现简单不易出错.
func median9(window *[9]uint8) uint8 {
	for i := 1; i < len(window); i++ {
		value := window[i]
		j := i - 1
		for j >= 0 && window[j] > value {
			window[j+1] = window[j]
			j--
		}
		window[j+1] = value
	}
	return window[4]
}
