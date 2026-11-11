package clipboard

import (
	"errors"
	"testing"
)

// TestEnsureInitIsStable 校验重复调用初始化结果一致.
//
// 底层库会与窗口系统建立连接, 已初始化的结果必须缓存, 否则每次读取都会
// 重复尝试; 在没有图形界面的环境里还会重复产生同样的错误.
func TestEnsureInitIsStable(t *testing.T) {
	first := ensureInit()
	second := ensureInit()
	if (first == nil) != (second == nil) {
		t.Fatalf("两次初始化的结果不一致: %v / %v", first, second)
	}
}

// TestImageOrTextReturnsBytesOrKnownError 校验读取的结果形态可控.
//
// 这个测试不假设剪贴板里一定有内容 (CI 与无人值守环境里可能为空),
// 只要求两种情况之一: 拿到非空字节, 或者得到可识别的错误.
func TestImageOrTextReturnsBytesOrKnownError(t *testing.T) {
	data, err := Image()
	switch {
	case err == nil:
		if len(data) == 0 {
			t.Fatal("没有错误时应返回非空字节")
		}
		// 库承诺 FmtImage 返回 PNG, 因此文件头必须是 PNG 魔数.
		if len(data) < 8 || string(data[1:4]) != "PNG" {
			t.Fatalf("返回的数据不是 PNG: %q", data[:min(8, len(data))])
		}
	case errors.Is(err, ErrImageUnavailable), errors.Is(err, ErrUnsupported):
		// 这两种都属于预期情况.
	default:
		t.Fatalf("出现未预期的错误类型: %v", err)
	}
}

// TestTextReturnsBytesOrKnownError 校验文本读取的错误类型受控.
func TestTextReturnsBytesOrKnownError(t *testing.T) {
	data, err := Text()
	switch {
	case err == nil:
		// 文本可能为空, 这是合法的.
		_ = data
	case errors.Is(err, ErrUnsupported):
		// 没有图形界面时属于预期情况.
	default:
		t.Fatalf("出现未预期的错误类型: %v", err)
	}
}

// min 是测试内使用的小工具, 避免为一行比较引入额外依赖.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
