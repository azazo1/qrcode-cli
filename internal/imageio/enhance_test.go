package imageio

import (
	"image"
	"image/color"
	"testing"
)

// TestToGrayUsesLumaWeights 校验灰度化按亮度加权, 而不是简单取平均.
func TestToGrayUsesLumaWeights(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})

	gray := ToGray(img)
	if len(gray.Pix) == 0 {
		t.Fatal("灰度图为空")
	}
	// 纯红的亮度约为 0.299*255 = 76, 明显低于简单平均的 85.
	got := gray.Pix[0]
	if got < 70 || got > 82 {
		t.Fatalf("纯红的灰度值 = %d, 期望在 70..82 之间", got)
	}
}

// TestStretchContrastExpandsRange 校验低对比度图片会被拉伸到全量程.
func TestStretchContrastExpandsRange(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 10, 10))
	for i := range img.Pix {
		if i%2 == 0 {
			img.Pix[i] = 110
		} else {
			img.Pix[i] = 190
		}
	}

	out := StretchContrast(img)
	minV, maxV := uint8(255), uint8(0)
	for _, v := range out.Pix {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if minV > 8 || maxV < 247 {
		t.Fatalf("拉伸后范围 = %d..%d, 期望接近 0..255", minV, maxV)
	}
}

// TestStretchContrastSkipsNarrowSpan 校验跨度过小时不放大噪声.
func TestStretchContrastSkipsNarrowSpan(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 128
	}
	out := StretchContrast(img)
	for i, v := range out.Pix {
		if v != 128 {
			t.Fatalf("像素 %d = %d, 期望保持 128", i, v)
		}
	}
}

// TestMedian3RemovesSaltPepper 校验中值滤波去掉孤立噪点.
func TestMedian3RemovesSaltPepper(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 7, 7))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	// 中心放一个孤立黑点.
	img.Pix[3*7+3] = 0

	out := Median3(img)
	if got := out.Pix[3*7+3]; got != 200 {
		t.Fatalf("孤立噪点未被去除, 中心像素 = %d", got)
	}
}

// TestMedian3PreservesEdges 校验中值滤波保留大块边缘, 不像均值模糊那样变灰.
func TestMedian3PreservesEdges(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 9, 9))
	for y := 0; y < 9; y++ {
		for x := 0; x < 9; x++ {
			if x < 4 {
				img.SetGray(x, y, color.Gray{Y: 0})
			} else {
				img.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}

	out := Median3(img)
	// 边缘两侧各留一格过渡, 更远处必须保持原值.
	for y := 0; y < 9; y++ {
		if got := out.GrayAt(1, y).Y; got != 0 {
			t.Fatalf("左区 (1,%d) = %d, 期望 0", y, got)
		}
		if got := out.GrayAt(8, y).Y; got != 255 {
			t.Fatalf("右区 (8,%d) = %d, 期望 255", y, got)
		}
	}
}

// TestMedian3TinyImage 校验过小的图片不会被越界处理.
func TestMedian3TinyImage(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	img.Pix[0] = 42
	out := Median3(img)
	if out.Pix[0] != 42 {
		t.Fatal("小图应原样返回")
	}
}

// TestMedian9 用暴力排序结果校验中值选取.
func TestMedian9(t *testing.T) {
	cases := [][9]uint8{
		{9, 8, 7, 6, 5, 4, 3, 2, 1},
		{0, 0, 0, 0, 255, 255, 255, 255, 255},
		{128, 128, 128, 128, 128, 128, 128, 128, 128},
	}
	for _, c := range cases {
		window := c
		got := median9(&window)
		// 暴力法求中值作为参照.
		want := c
		for i := 0; i < 9; i++ {
			for j := i + 1; j < 9; j++ {
				if want[j] < want[i] {
					want[i], want[j] = want[j], want[i]
				}
			}
		}
		if got != want[4] {
			t.Fatalf("median9(%v) = %d, want %d", c, got, want[4])
		}
	}
}

// TestEnhanceReturnsGray 校验增强结果是可以直接交给二维码识别的灰度图.
func TestEnhanceReturnsGray(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			if (x/4+y/4)%2 == 0 {
				img.SetRGBA(x, y, color.RGBA{110, 110, 110, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{190, 190, 190, 255})
			}
		}
	}

	out := Enhance(img)
	if out.Bounds().Dx() != 20 || out.Bounds().Dy() != 20 {
		t.Fatalf("尺寸被改变: %v", out.Bounds())
	}
	// 拉伸之后应当同时出现接近纯黑与纯白的像素.
	minV, maxV := uint8(255), uint8(0)
	for _, v := range out.Pix {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if minV > 20 || maxV < 235 {
		t.Fatalf("增强后范围 = %d..%d, 期望接近 0..255", minV, maxV)
	}
}
