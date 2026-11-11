package decode

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"math/rand"
	"testing"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/azazo1/qrcode-cli/internal/imageio"
)

// renderQR 把文本渲染成指定像素大小的二维码图片.
func renderQR(t *testing.T, text string, size int) image.Image {
	t.Helper()

	data, err := qrcode.Encode(text, qrcode.Medium, size)
	if err != nil {
		t.Fatalf("生成二维码失败: %v", err)
	}
	img, _, err := imageio.Decode(data)
	if err != nil {
		t.Fatalf("解码生成的二维码失败: %v", err)
	}
	return img
}

// TestDecodeSingle 是最基本的往返验证: 编码后能原样解回.
func TestDecodeSingle(t *testing.T) {
	const text = "https://example.com/roundtrip"
	img := renderQR(t, text, 400)

	results, err := New(Options{}).Decode(context.Background(), img)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("结果数 = %d, want 1", len(results))
	}
	if results[0].Text != text {
		t.Fatalf("内容 = %q, want %q", results[0].Text, text)
	}
}

// TestDecodeMultiple 验证一张图里的多个二维码全部被识别, 且顺序按阅读顺序.
func TestDecodeMultiple(t *testing.T) {
	type placement struct {
		text string
		at   image.Point
	}
	placements := []placement{
		{"https://first.example", image.Pt(20, 20)},
		{"second-qr-code", image.Pt(420, 20)},
		{"third-内容", image.Pt(820, 20)},
	}

	canvas := image.NewRGBA(image.Rect(0, 0, 1200, 400))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	for _, p := range placements {
		img := renderQR(t, p.text, 350)
		draw.Draw(canvas, image.Rect(p.at.X, p.at.Y, p.at.X+350, p.at.Y+350), img, image.Point{}, draw.Over)
	}

	results, err := New(Options{}).Decode(context.Background(), canvas)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != len(placements) {
		t.Fatalf("结果数 = %d, want %d (实际内容: %v)", len(results), len(placements), texts(results))
	}
	// 三张图横向排列, 期望从左到右.
	for i, p := range placements {
		if results[i].Text != p.text {
			t.Fatalf("第 %d 条 = %q, want %q (全部: %v)", i, results[i].Text, p.text, texts(results))
		}
	}
}

// TestDecodeInverted 验证深底浅码的反色图仍能被识别.
func TestDecodeInverted(t *testing.T) {
	const text = "inverted-qr-9931"
	src := renderQR(t, text, 400)

	b := src.Bounds()
	inverted := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(inverted, inverted.Bounds(), src, b.Min, draw.Src)
	for i := 0; i < len(inverted.Pix); i += 4 {
		inverted.Pix[i+0] = 255 - inverted.Pix[i+0]
		inverted.Pix[i+1] = 255 - inverted.Pix[i+1]
		inverted.Pix[i+2] = 255 - inverted.Pix[i+2]
	}

	results, err := New(Options{}).Decode(context.Background(), inverted)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != 1 || results[0].Text != text {
		t.Fatalf("结果 = %v, want [%q]", texts(results), text)
	}
}

// TestDecodeSmallImage 验证小尺寸图片经放大预处理后仍可识别.
func TestDecodeSmallImage(t *testing.T) {
	const text = "tiny-qr-4412"
	img := renderQR(t, text, 120)

	results, err := New(Options{}).Decode(context.Background(), img)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != 1 || results[0].Text != text {
		t.Fatalf("结果 = %v, want [%q]", texts(results), text)
	}
}

// TestDecodeNoCode 验证没有二维码时返回空结果而不是错误.
func TestDecodeNoCode(t *testing.T) {
	blank := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(blank, blank.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	results, err := New(Options{}).Decode(context.Background(), blank)
	if err != nil {
		t.Fatalf("Decode 不应报错: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("结果数 = %d, want 0", len(results))
	}
}

// TestDedupe 校验去重: 同一位置同一内容只保留一次, 不同位置的相同内容都保留.
func TestDedupe(t *testing.T) {
	same := Rect{X: 10, Y: 10, Width: 100, Height: 100}
	overlapping := Rect{X: 12, Y: 12, Width: 100, Height: 100}
	apart := Rect{X: 500, Y: 500, Width: 100, Height: 100}

	results := dedupe([]Result{
		{Text: "a", Bounds: same},
		{Text: "a", Bounds: overlapping},
		{Text: "a", Bounds: apart},
		{Text: "b", Bounds: same},
	})

	if len(results) != 3 {
		t.Fatalf("结果数 = %d, want 3 (实际: %v)", len(results), results)
	}
	countA := 0
	for _, r := range results {
		if r.Text == "a" {
			countA++
		}
	}
	if countA != 2 {
		t.Fatalf("内容 a 出现 %d 次, want 2", countA)
	}
}

// TestSortReadingOrder 校验多行多列的阅读顺序.
func TestSortReadingOrder(t *testing.T) {
	results := []Result{
		{Text: "bottom-right", Bounds: Rect{X: 500, Y: 500, Width: 100, Height: 100}},
		{Text: "top-right", Bounds: Rect{X: 500, Y: 100, Width: 100, Height: 100}},
		{Text: "top-left", Bounds: Rect{X: 100, Y: 100, Width: 100, Height: 100}},
		{Text: "bottom-left", Bounds: Rect{X: 100, Y: 500, Width: 100, Height: 100}},
	}

	sortReadingOrder(results)

	want := []string{"top-left", "top-right", "bottom-left", "bottom-right"}
	for i, w := range want {
		if results[i].Text != w {
			t.Fatalf("第 %d 条 = %q, want %q", i, results[i].Text, w)
		}
	}
}

// texts 提取结果文本, 便于断言失败时输出可读信息.
func texts(results []Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Text)
	}
	return out
}

// TestDecodeNoisyImage 验证椒盐噪声图片能通过增强变体识别出来.
//
// 纯 ZXing 对散落噪点很敏感, 这一步依赖灰度化与中值滤波.
func TestDecodeNoisyImage(t *testing.T) {
	const text = "noisy-qr-7788"
	base := renderQR(t, text, 400)
	b := base.Bounds()

	noisy := image.NewRGBA(b)
	draw.Draw(noisy, b, base, image.Point{}, draw.Src)

	rnd := rand.New(rand.NewSource(42))
	for i := 0; i < 1500; i++ {
		x, y := rnd.Intn(b.Dx()), rnd.Intn(b.Dy())
		noisy.Set(x, y, color.RGBA{
			R: uint8(rnd.Intn(256)),
			G: uint8(rnd.Intn(256)),
			B: uint8(rnd.Intn(256)),
			A: 255,
		})
	}

	results, err := New(Options{}).Decode(context.Background(), noisy)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != 1 || results[0].Text != text {
		t.Fatalf("结果 = %v, want [%q]", texts(results), text)
	}
}

// TestDecodeLowContrast 验证低对比度图片能通过对比度拉伸识别出来.
func TestDecodeLowContrast(t *testing.T) {
	const text = "low-contrast-6612"
	base := renderQR(t, text, 400)
	b := base.Bounds()

	// 灰底 (亮度 200) 配深灰码 (亮度 130), 对比度只有 70.
	low := image.NewRGBA(b)
	draw.Draw(low, b, &image.Uniform{color.RGBA{200, 200, 200, 255}}, image.Point{}, draw.Src)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if r, _, _, _ := base.At(x, y).RGBA(); r < 0x8000 {
				low.Set(x, y, color.RGBA{130, 130, 130, 255})
			}
		}
	}

	results, err := New(Options{}).Decode(context.Background(), low)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != 1 || results[0].Text != text {
		t.Fatalf("结果 = %v, want [%q]", texts(results), text)
	}
}

// TestDecodeNoPreprocessStillWorks 校验关闭预处理时, 干净的图仍然可识别.
//
// 这保证 NoPreprocess 不会把常规用法弄坏, 只是失去增强能力.
func TestDecodeNoPreprocessStillWorks(t *testing.T) {
	const text = "no-preprocess-5533"
	img := renderQR(t, text, 400)

	results, err := New(Options{NoPreprocess: true}).Decode(context.Background(), img)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != 1 || results[0].Text != text {
		t.Fatalf("结果 = %v, want [%q]", texts(results), text)
	}
}

// TestDecodeMultiCodeWithOneDegraded 验证多码图中质量不一致时不会漏码.
//
// 这是增强变体必须无条件执行的原因: 清晰的那几个码会被常规变体识别出来,
// 有噪声的那一个只能靠增强变体. 如果常规变体一旦"找到了东西"就跳过增强,
// 这个码会被静默漏掉, 而多码识别正是本工具的核心能力.
func TestDecodeMultiCodeWithOneDegraded(t *testing.T) {
	type placement struct {
		text  string
		at    image.Point
		noisy bool
	}
	placements := []placement{
		{text: "clean-alpha", at: image.Pt(20, 20)},
		{text: "clean-beta", at: image.Pt(420, 20)},
		{text: "noisy-gamma", at: image.Pt(820, 20), noisy: true},
	}

	canvas := image.NewRGBA(image.Rect(0, 0, 1200, 400))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	for _, p := range placements {
		img := renderQR(t, p.text, 350)
		draw.Draw(canvas, image.Rect(p.at.X, p.at.Y, p.at.X+350, p.at.Y+350), img, image.Point{}, draw.Over)
	}

	// 只在第三个码的区域内撒噪点, 让三个码的质量产生差异.
	rnd := rand.New(rand.NewSource(5))
	region := image.Rect(820, 20, 1170, 370)
	for i := 0; i < 800; i++ {
		x := region.Min.X + rnd.Intn(region.Dx())
		y := region.Min.Y + rnd.Intn(region.Dy())
		canvas.Set(x, y, color.RGBA{
			R: uint8(rnd.Intn(256)),
			G: uint8(rnd.Intn(256)),
			B: uint8(rnd.Intn(256)),
			A: 255,
		})
	}

	results, err := New(Options{}).Decode(context.Background(), canvas)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(results) != len(placements) {
		t.Fatalf("结果数 = %d, want %d (实际: %v)", len(results), len(placements), texts(results))
	}

	found := map[string]bool{}
	for _, r := range results {
		found[r.Text] = true
	}
	for _, p := range placements {
		if !found[p.text] {
			t.Fatalf("漏掉了 %q (实际: %v)", p.text, texts(results))
		}
	}
}
