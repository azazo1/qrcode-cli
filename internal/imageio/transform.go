package imageio

import (
	"image"

	"golang.org/x/image/draw"
)

// MaxSide 是参与解码的原图长边上限.
// 过大的截图会显著拖慢解码, 超过时先等比缩小到该长度.
const MaxSide = 3000

// MinShortSide 是解码前希望达到的短边长度.
// 过小的二维码图片 (例如图标尺寸) 识别率很低, 需要整数倍放大.
const MinShortSide = 512

// MaxScale 是预处理放大的最大整数倍, 防止小图标被放大到不可控的尺寸.
const MaxScale = 4

// Size 返回图像的宽高.
func Size(img image.Image) (width, height int) {
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

// ScaleFactor 计算为了达到 MinShortSide 需要的整数放大倍数.
// 返回 1 表示不需要放大.
func ScaleFactor(width, height int) int {
	short := width
	if height < short {
		short = height
	}
	if short <= 0 {
		return 1
	}

	factor := 1
	for factor < MaxScale && short*factor < MinShortSide {
		factor++
	}
	return factor
}

// Shrink 在长边超过 MaxSide 时等比缩小, 否则原样返回.
// 缩小使用近似双线性插值, 避免锯齿影响定位.
func Shrink(img image.Image) image.Image {
	width, height := Size(img)
	long := width
	if height > long {
		long = height
	}
	if long <= MaxSide {
		return img
	}

	ratio := float64(MaxSide) / float64(long)
	dst := image.NewRGBA(image.Rect(0, 0, int(float64(width)*ratio), int(float64(height)*ratio)))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}

// Upscale 按整数倍最近邻放大图像, factor <= 1 时原样返回.
// 最近邻不会引入新的灰度过渡, 对二值化的二维码更友好.
func Upscale(img image.Image, factor int) image.Image {
	if factor <= 1 {
		return img
	}

	src := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, src.Dx()*factor, src.Dy()*factor))
	draw.NearestNeighbor.Scale(dst, dst.Bounds(), img, src, draw.Over, nil)
	return dst
}

// Invert 返回反色后的图像副本.
// 深色背景上的浅色二维码需要反色后才能被常规识别器处理.
func Invert(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)

	for i := 0; i < len(dst.Pix); i += 4 {
		dst.Pix[i+0] = 255 - dst.Pix[i+0]
		dst.Pix[i+1] = 255 - dst.Pix[i+1]
		dst.Pix[i+2] = 255 - dst.Pix[i+2]
	}
	return dst
}
