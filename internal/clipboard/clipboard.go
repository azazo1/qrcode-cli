// Package clipboard 从系统剪贴板读取图片或文本.
//
// 底层使用 golang.design/x/clipboard: 它通过 purego 动态加载系统库,
// 在桌面平台上不需要 cgo, 也不需要外部的 wl-paste, xclip 或 PowerShell.
// 这样剪贴板读取只是几次内存调用, 而不是频繁启动子进程.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.design/x/clipboard"
)

// ErrImageUnavailable 表示剪贴板里没有图片.
//
// 这是常见情况 (例如只复制了一段文字), 调用方应当据此退回文本处理,
// 而不是当成故障.
var ErrImageUnavailable = errors.New("剪贴板中没有图片")

// ErrUnsupported 表示当前平台或环境不支持读取剪贴板.
var ErrUnsupported = errors.New("当前平台不支持读取剪贴板")

// initOnce 与 initErr 保证底层库只初始化一次.
//
// 库的 Init 会与窗口系统建立连接, 在无图形界面的服务器上必然失败;
// 缓存结果避免每次读取都重复尝试并重复产生同样的错误.
var (
	initOnce sync.Once
	initErr  error
)

// ensureInit 初始化底层剪贴板连接.
func ensureInit() error {
	initOnce.Do(func() {
		if err := clipboard.Init(); err != nil {
			outErr := fmt.Errorf("%w: %v", ErrUnsupported, err)
			// 没有图形界面是环境问题, 不是程序缺陷, 因此统一归为不支持.
			if errors.Is(err, clipboard.ErrUnavailable) {
				initErr = outErr
				return
			}
			initErr = outErr
		}
	})
	return initErr
}

// Image 读取剪贴板中的图片, 返回 PNG 编码的字节.
//
// 剪贴板没有图片时返回 ErrImageUnavailable.
func Image() ([]byte, error) {
	if err := ensureInit(); err != nil {
		return nil, err
	}

	data, err := clipboard.Read(context.Background(), clipboard.FmtImage)
	if err != nil {
		if errors.Is(err, clipboard.ErrNoData) {
			return nil, ErrImageUnavailable
		}
		if errors.Is(err, clipboard.ErrUnsupported) || errors.Is(err, clipboard.ErrUnavailable) {
			return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		return nil, fmt.Errorf("读取剪贴板图片失败: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrImageUnavailable
	}
	return data, nil
}

// Text 读取剪贴板中的文本.
// 剪贴板没有文本时返回空切片和 nil 错误.
func Text() ([]byte, error) {
	if err := ensureInit(); err != nil {
		return nil, err
	}

	data, err := clipboard.Read(context.Background(), clipboard.FmtText)
	if err != nil {
		// 没有文本不算故障, 由调用方根据空内容自行判断.
		if errors.Is(err, clipboard.ErrNoData) {
			return nil, nil
		}
		if errors.Is(err, clipboard.ErrUnsupported) || errors.Is(err, clipboard.ErrUnavailable) {
			return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		return nil, fmt.Errorf("读取剪贴板文本失败: %w", err)
	}
	return data, nil
}
