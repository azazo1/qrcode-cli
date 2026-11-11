// Package decode 负责从图片中识别二维码.
//
// 解码器被抽象成 Decoder 接口, 当前唯一实现基于 gozxing (ZXing 的纯 Go 移植).
// 把接口留窄, 以后接入 OpenCV wechat_qrcode 之类的引擎无需改动调用方.
package decode

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"sort"

	"github.com/makiuchi-d/gozxing"
	zxingmulti "github.com/makiuchi-d/gozxing/multi/qrcode"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/azazo1/qrcode-cli/internal/imageio"
)

// Rect 是二维码在图片中的像素边界.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Result 是一个被识别出来的二维码.
type Result struct {
	Text    string `json:"text"`
	ECLevel string `json:"ec_level,omitempty"`
	Bounds  Rect   `json:"bounds"`
}

// Decoder 从单张图片中识别所有二维码.
// 没有识别到任何码时返回空切片和 nil 错误.
type Decoder interface {
	Decode(ctx context.Context, img image.Image) ([]Result, error)
}

// Options 控制解码时的预处理行为.
type Options struct {
	// NoPreprocess 关闭放大与反色等变体, 只尝试原图.
	NoPreprocess bool
}

// New 按选项创建解码器.
func New(opts Options) Decoder {
	return &zxingDecoder{opts: opts}
}

// zxingDecoder 基于 gozxing 的多码识别实现.
type zxingDecoder struct {
	opts Options
}

// Decode 依次尝试若干图像变体, 合并所有命中结果.
//
// 分两轮执行:
//
//  1. 常规变体: 原图, 反色, 以及小图放大后的对应版本. 代价低, 覆盖绝大多数场景.
//  2. 增强变体: 灰度 + 对比度拉伸 + 中值滤波. 代价明显更高, 只在常规变体
//     一无所获时启用, 用于低对比度和有噪声的图片.
//
// 常规变体之间会互相累积结果, 因为同一张图里可能存在只有反色后才能识别出的码.
func (d *zxingDecoder) Decode(ctx context.Context, img image.Image) ([]Result, error) {
	base := imageio.Shrink(img)
	// 所有变体都在缩小后的基准图上产生, 结果坐标最后要换算回原始输入图像.
	shrinkRatio := scaleBetween(img, base)

	// factor 是让小图更容易识别所需的整数放大倍数, 1 表示不需要放大.
	factor := 1
	variants := []image.Image{base}
	if !d.opts.NoPreprocess {
		width, height := imageio.Size(base)
		factor = imageio.ScaleFactor(width, height)
		if factor > 1 {
			scaled := imageio.Upscale(base, factor)
			variants = append(variants, imageio.Invert(base), scaled, imageio.Invert(scaled))
		} else {
			variants = append(variants, imageio.Invert(base))
		}
	}

	all := d.runVariants(ctx, variants, base)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 增强变体必须无条件执行, 不能因为常规变体已有结果就跳过.
	//
	// 一张图里多个二维码的质量可能不一致: 清晰的那几个会被常规变体识别出来,
	// 而有噪声或低对比度的那一个只能靠增强变体. 只要常规变体"找到了东西"就
	// 跳过增强, 就会静默漏掉这个码, 用户看到的数量少于图片里实际的数量.
	if !d.opts.NoPreprocess {
		extra := d.runVariants(ctx, enhancedVariants(base, factor), base)
		if len(all) == 0 && len(extra) > 0 {
			slog.Debug("常规变体未识别到二维码, 由增强变体命中")
		}
		all = append(all, extra...)
	}

	// 基准图相对原始输入可能被缩小过, 这里统一换算回去.
	if shrinkRatio != 1 {
		for i := range all {
			all[i].Bounds = rescale(all[i].Bounds, shrinkRatio)
		}
	}

	return dedupe(all), nil
}

// enhancedVariants 构造增强变体.
//
// 先去噪再放大: 最近邻放大会把单个噪点复制成 2x2 的块, 3x3 中值滤波无法
// 清除这种块, 反而让去噪失效. 因此 Enhance 必须作用在原生尺度上.
func enhancedVariants(base image.Image, factor int) []image.Image {
	enhanced := imageio.Enhance(base)

	variants := []image.Image{enhanced}
	if factor > 1 {
		variants = append(variants, imageio.Upscale(enhanced, factor))
	}
	variants = append(variants, imageio.Invert(enhanced))

	return variants
}

// runVariants 逐个识别变体并合并结果, 同时把坐标换算回基准图尺度.
func (d *zxingDecoder) runVariants(ctx context.Context, variants []image.Image, base image.Image) []Result {
	var all []Result
	for _, variant := range variants {
		if ctx.Err() != nil {
			return all
		}

		results, err := decodeVariant(variant)
		if err != nil {
			// 单个变体的失败 (例如二值化失败) 不应中断整体流程.
			continue
		}

		// 放大或灰度化后的坐标需要还原到基准图尺度, 便于跨变体去重.
		if scale := scaleBetween(variant, base); scale != 1 {
			for j := range results {
				results[j].Bounds = rescale(results[j].Bounds, scale)
			}
		}
		all = append(all, results...)
	}
	return all
}

// decodeVariant 对单个图像变体执行多码识别, 并在多码失败时退回单码识别.
func decodeVariant(img image.Image) ([]Result, error) {
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil, fmt.Errorf("构建二值图失败: %w", err)
	}

	multi := zxingmulti.NewQRCodeMultiReader()
	detected, err := multi.DecodeMultipleWithoutHint(bitmap)
	if err != nil {
		// ReaderException 表示这一变体里没有可识别的码, 属于正常情况.
		if _, ok := err.(gozxing.ReaderException); ok {
			return nil, nil
		}
		return nil, err
	}
	if len(detected) > 0 {
		return toResults(detected), nil
	}

	// 多码检测器对单码有时不如单码 reader 稳定, 这里补一次兜底.
	single, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		if _, ok := err.(gozxing.ReaderException); ok {
			return nil, nil
		}
		return nil, err
	}
	return toResults([]*gozxing.Result{single}), nil
}

// toResults 把 gozxing 的结果转换成内部结构.
func toResults(raw []*gozxing.Result) []Result {
	results := make([]Result, 0, len(raw))
	for _, r := range raw {
		results = append(results, Result{
			Text:    r.GetText(),
			ECLevel: ecLevelOf(r),
			Bounds:  boundsOf(r),
		})
	}
	return results
}

// ecLevelOf 从结果元数据里取出纠错等级.
func ecLevelOf(r *gozxing.Result) string {
	metadata := r.GetResultMetadata()
	if metadata == nil {
		return ""
	}
	level, ok := metadata[gozxing.ResultMetadataType_ERROR_CORRECTION_LEVEL]
	if !ok {
		return ""
	}
	text, _ := level.(string)
	return text
}

// boundsOf 由四个定位点推算二维码的外接矩形.
// gozxing 给出的是检测到的角点, 不一定与码的四角严格对齐.
func boundsOf(r *gozxing.Result) Rect {
	points := r.GetResultPoints()
	if len(points) == 0 {
		return Rect{}
	}

	minX, minY := points[0].GetX(), points[0].GetY()
	maxX, maxY := minX, minY
	for _, p := range points[1:] {
		x, y := p.GetX(), p.GetY()
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
	}
	return Rect{
		X:      int(minX),
		Y:      int(minY),
		Width:  int(maxX - minX),
		Height: int(maxY - minY),
	}
}

// dedupe 去掉不同变体重复识别出的同一个码, 并按阅读顺序排序.
//
// 判定重复的条件是内容相同且边界框重叠度较高: 同一位置上的同一内容只保留一次,
// 而内容相同的两个不同位置的码 (例如页面里同一条链接出现了两次) 都要保留.
func dedupe(results []Result) []Result {
	kept := make([]Result, 0, len(results))
	for _, candidate := range results {
		duplicate := false
		for _, existing := range kept {
			if existing.Text != candidate.Text {
				continue
			}
			if iou(existing.Bounds, candidate.Bounds) > 0.5 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			kept = append(kept, candidate)
		}
	}

	sortReadingOrder(kept)
	return kept
}

// iou 计算两个矩形的交并比, 度量范围 0..1.
func iou(a, b Rect) float64 {
	interLeft := max(a.X, b.X)
	interTop := max(a.Y, b.Y)
	interRight := min(a.X+a.Width, b.X+b.Width)
	interBottom := min(a.Y+a.Height, b.Y+b.Height)

	if interRight <= interLeft || interBottom <= interTop {
		return 0
	}
	interArea := float64((interRight - interLeft) * (interBottom - interTop))
	union := float64(a.Width*a.Height + b.Width*b.Height)
	if union <= interArea {
		return 0
	}
	return interArea / (union - interArea)
}

// sortReadingOrder 按 "从上到下, 从左到右" 稳定排序.
//
// 先把中心纵坐标相近的码聚成一行, 行内按横坐标排序. 直接按 y 排序会把
// 同一行里高度略有差异的码拆成两行, 顺序变得难以预期.
func sortReadingOrder(results []Result) {
	if len(results) < 2 {
		return
	}

	// 用所有码高度中位数的一半作为同一行的容差, 至少 1 像素.
	tolerance := rowTolerance(results)

	sort.SliceStable(results, func(i, j int) bool {
		ci, cj := centerY(results[i]), centerY(results[j])
		if abs(ci-cj) > tolerance {
			return ci < cj
		}
		return centerX(results[i]) < centerX(results[j])
	})
}

// rowTolerance 由结果高度的中位数推算行内容差.
func rowTolerance(results []Result) int {
	heights := make([]int, 0, len(results))
	for _, r := range results {
		heights = append(heights, r.Bounds.Height)
	}
	sort.Ints(heights)
	median := heights[len(heights)/2]

	if median/2 < 1 {
		return 1
	}
	return median / 2
}

func centerX(r Result) int { return r.Bounds.X + r.Bounds.Width/2 }

func centerY(r Result) int { return r.Bounds.Y + r.Bounds.Height/2 }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// scaleBetween 估算缩放变体相对于基准图的放大倍数.
func scaleBetween(variant, base image.Image) float64 {
	vw, _ := imageio.Size(variant)
	bw, bh := imageio.Size(base)
	_ = bh
	if bw <= 0 {
		return 1
	}
	return float64(vw) / float64(bw)
}

// rescale 把变体上的坐标换算回基准图坐标.
func rescale(r Rect, scale float64) Rect {
	if scale <= 0 || scale == 1 {
		return r
	}
	return Rect{
		X:      int(float64(r.X) / scale),
		Y:      int(float64(r.Y) / scale),
		Width:  int(float64(r.Width) / scale),
		Height: int(float64(r.Height) / scale),
	}
}
