package webui

import (
	"context"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azazo1/qrcode-cli/internal/encode"
)

// startTestServer 起一个监听随机端口的服务, 测试结束自动关闭.
func startTestServer(t *testing.T, content string) string {
	t.Helper()

	m, err := encode.Build(content, encode.LevelMedium)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pngData, err := m.RenderPNG(4)
	if err != nil {
		t.Fatalf("RenderPNG: %v", err)
	}

	server, url, err := Start("127.0.0.1:0", m, pngData)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = server.Wait(ctx)
	})
	return url
}

// TestIndexPage 校验首页返回 HTML 且包含二维码内容.
func TestIndexPage(t *testing.T) {
	const content = "webui-test-content"
	url := startTestServer(t, content)

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	if !strings.Contains(string(body), content) {
		t.Fatal("页面应包含原始内容")
	}
	if !strings.Contains(string(body), "data:image/png;base64,") {
		t.Fatal("页面应内联 PNG 数据")
	}
}

// TestQRPng 校验图片路由返回可解码的 PNG.
func TestQRPng(t *testing.T) {
	url := startTestServer(t, "png-route-test")

	resp, err := http.Get(url + "qr.png")
	if err != nil {
		t.Fatalf("GET /qr.png: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q", ct)
	}

	if _, err := png.Decode(resp.Body); err != nil {
		t.Fatalf("响应不是有效 PNG: %v", err)
	}
}

// TestWaitShutdown 校验 ctx 取消后服务会真正停止.
func TestWaitShutdown(t *testing.T) {
	m, err := encode.Build("shutdown-test", encode.LevelLow)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pngData, err := m.RenderPNG(2)
	if err != nil {
		t.Fatalf("RenderPNG: %v", err)
	}

	server, url, err := Start("127.0.0.1:0", m, pngData)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = server.Wait(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("取消后服务未能在限定时间内关闭")
	}

	// 关闭后不应再接受连接.
	client := http.Client{Timeout: 500 * time.Millisecond}
	if resp, err := client.Get(url); err == nil {
		resp.Body.Close()
		t.Fatal("服务关闭后仍能访问")
	}
}

// TestBrowseURL 校验监听全部地址时给出可点开的回环地址.
func TestBrowseURL(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want string
	}{
		{"指定回环", "127.0.0.1:0", "http://127.0.0.1:"},
		{"全部地址 IPv4", "0.0.0.0:0", "http://127.0.0.1:"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := encode.Build("browse-url-test", encode.LevelLow)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			pngData, err := m.RenderPNG(2)
			if err != nil {
				t.Fatalf("RenderPNG: %v", err)
			}

			server, url, err := Start(c.addr, m, pngData)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_ = server.Wait(ctx)
			}()

			if !strings.HasPrefix(url, c.want) {
				t.Fatalf("URL = %q, want 前缀 %q", url, c.want)
			}
			// 地址必须能真正访问.
			resp, err := http.Get(url)
			if err != nil {
				t.Fatalf("访问 %s: %v", url, err)
			}
			resp.Body.Close()
		})
	}
}
