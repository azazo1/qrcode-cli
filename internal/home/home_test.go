package home

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/azazo1/qrcode-cli/internal/encode"
)

// startTestServer 起一个监听随机端口的入口服务, 测试结束自动关闭.
func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	server, base, err := Start("127.0.0.1:0", Options{OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = server.Wait(ctx)
	})
	return server, base
}

// get 发起一次 GET 请求并返回响应体.
func get(t *testing.T, rawURL string) (int, http.Header, string) {
	t.Helper()

	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

// post 发起一次 POST 请求并返回状态码与响应体.
func post(t *testing.T, rawURL string, body []byte) (int, string) {
	t.Helper()

	resp, err := http.Post(rawURL, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", rawURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	return resp.StatusCode, string(data)
}

// TestServesPages 确认主菜单, 编码页, 解码页与挂载的子页面都能打开.
func TestServesPages(t *testing.T) {
	_, base := startTestServer(t)

	cases := []struct {
		path     string
		contains string
	}{
		{path: "/", contains: "cimbar"},
		{path: "/encode", contains: "api/encode"},
		{path: "/decode", contains: "api/decode"},
		{path: "/cam/", contains: "frame"},
		{path: "/cimbar/", contains: "index.html"},
	}
	for _, c := range cases {
		code, _, body := get(t, base+c.path)
		if code != http.StatusOK {
			t.Fatalf("%s 状态码 = %d", c.path, code)
		}
		if !strings.Contains(body, c.contains) {
			t.Fatalf("%s 的内容不含 %q", c.path, c.contains)
		}
	}

	// 共享样式要在根路径与子页面下都能取到.
	for _, path := range []string{"/assets/style.css", "/cam/assets/style.css", "/cimbar/assets/style.css"} {
		code, header, _ := get(t, base+path)
		if code != http.StatusOK {
			t.Fatalf("%s 状态码 = %d", path, code)
		}
		if got := header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
			t.Fatalf("%s 的类型 = %q", path, got)
		}
	}

	// 子路径不带尾斜杠时要跳到带斜杠的地址, 页面里的相对请求才能正确解析.
	resp, err := http.Get(base + "cam")
	if err != nil {
		t.Fatalf("GET /cam: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cam 状态码 = %d", resp.StatusCode)
	}
}

// TestEncodeAPI 确认编码接口返回可以直接展示的 PNG.
func TestEncodeAPI(t *testing.T) {
	_, base := startTestServer(t)

	code, body := post(t, base+"api/encode", []byte("https://example.com"))
	if code != http.StatusOK {
		t.Fatalf("编码状态码 = %d, 响应 %s", code, body)
	}

	var result encodeResponse
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if !strings.HasPrefix(result.DataURI, "data:image/png;base64,") {
		t.Fatalf("DataURI = %q", result.DataURI)
	}
	if result.Content != "https://example.com" || result.Version <= 0 || result.Modules <= 0 {
		t.Fatalf("响应字段异常: %+v", result)
	}
}

// TestEncodeAPIRejectsEmpty 确认空内容被拒绝.
func TestEncodeAPIRejectsEmpty(t *testing.T) {
	_, base := startTestServer(t)

	code, _ := post(t, base+"api/encode", []byte("   "))
	if code != http.StatusBadRequest {
		t.Fatalf("空内容状态码 = %d, 期望 400", code)
	}
}

// TestDecodeAPI 确认解码接口能识别编码接口生成的二维码.
func TestDecodeAPI(t *testing.T) {
	_, base := startTestServer(t)

	matrix, err := encode.Build("hello 解码", encode.LevelMedium)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pngData, err := matrix.RenderPNG(6)
	if err != nil {
		t.Fatalf("RenderPNG: %v", err)
	}

	code, body := post(t, base+"api/decode", pngData)
	if code != http.StatusOK {
		t.Fatalf("解码状态码 = %d, 响应 %s", code, body)
	}

	var result decodeResponse
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Text != "hello 解码" {
		t.Fatalf("识别结果异常: %+v", result.Results)
	}
}

// TestDecodeAPIRejectsNonImage 确认不是图片的数据被拒绝.
func TestDecodeAPIRejectsNonImage(t *testing.T) {
	_, base := startTestServer(t)

	code, _ := post(t, base+"api/decode", []byte("这不是图片"))
	if code != http.StatusBadRequest {
		t.Fatalf("非图片状态码 = %d, 期望 400", code)
	}
}
