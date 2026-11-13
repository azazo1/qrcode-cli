package cam

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/azazo1/qrcode-cli/internal/decode"
	"github.com/azazo1/qrcode-cli/internal/imageio"
)

// qrFrame 生成一张二维码 JPEG, 模拟页面送来的画面帧.
func qrFrame(t *testing.T, text string) []byte {
	t.Helper()

	pngData, err := qrcode.Encode(text, qrcode.Medium, 400)
	if err != nil {
		t.Fatalf("生成二维码失败: %v", err)
	}
	img, _, err := imageio.Decode(pngData)
	if err != nil {
		t.Fatalf("解码 PNG: %v", err)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("编码 JPEG: %v", err)
	}
	return buf.Bytes()
}

// mirroredFrame 把帧水平翻转, 模拟镜像摄像头或镜面反射拍到的画面.
func mirroredFrame(t *testing.T, data []byte) []byte {
	t.Helper()

	img, _, err := imageio.Decode(data)
	if err != nil {
		t.Fatalf("解码帧: %v", err)
	}

	b := img.Bounds()
	flipped := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			flipped.Set(b.Max.X-1-x, y, img.At(x, y))
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flipped, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("编码 JPEG: %v", err)
	}
	return buf.Bytes()
}

// startTestServer 起一个监听随机端口的服务, 测试结束自动关闭.
func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	server, url, err := Start("127.0.0.1:0", Options{Decoder: decode.New(decode.Options{})})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = server.Wait(ctx)
	})
	return server, url
}

// postJSON 向路径提交请求体, 返回状态码与解码后的响应.
func postJSON(t *testing.T, url, path string, body []byte) (int, frameResponse) {
	t.Helper()

	resp, err := http.Post(url+path, "image/jpeg", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()

	var payload frameResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("解析响应: %v", err)
		}
	}
	return resp.StatusCode, payload
}

// TestDecodeFrameAndFinish 覆盖一次完整的扫码会话: 识别, 去重, 结束.
func TestDecodeFrameAndFinish(t *testing.T) {
	server, url := startTestServer(t)
	frame := qrFrame(t, "cam-session-code")

	status, first := postJSON(t, url, "/frame", frame)
	if status != http.StatusOK {
		t.Fatalf("首帧状态码 = %d", status)
	}
	if len(first.Codes) != 1 || first.Codes[0].Text != "cam-session-code" {
		t.Fatalf("首帧结果 = %+v", first.Codes)
	}

	// 同一个码在后续帧里会反复出现, 只应返回一次.
	status, second := postJSON(t, url, "/frame", frame)
	if status != http.StatusOK {
		t.Fatalf("次帧状态码 = %d", status)
	}
	if len(second.Codes) != 0 {
		t.Fatalf("重复内容不应再次返回: %+v", second.Codes)
	}

	resp, err := http.Get(url + "hits")
	if err != nil {
		t.Fatalf("GET /hits: %v", err)
	}
	defer resp.Body.Close()
	var hits hitsResponse
	if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
		t.Fatalf("解析 /hits: %v", err)
	}
	if len(hits.Hits) != 1 || hits.Hits[0].Text != "cam-session-code" {
		t.Fatalf("/hits = %+v", hits.Hits)
	}

	resp, err = http.Post(url+"finish", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /finish: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("结束状态码 = %d", resp.StatusCode)
	}

	// 结束后 Wait 必须立即返回, 不再等待 ctx.
	done := make(chan error, 1)
	go func() { done <- server.Wait(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("结束请求之后 Wait 仍未返回")
	}

	if got := server.Hits(); len(got) != 1 || got[0] != "cam-session-code" {
		t.Fatalf("Hits() = %v", got)
	}
	if got := server.Frames(); got != 2 {
		t.Fatalf("Frames() = %d, want 2", got)
	}
}

// TestDecodeMirroredFrame 验证镜像画面也能识别.
//
// 部分摄像头与镜面反射会给出左右翻转的画面, 识别必须能兜住,
// 不能要求使用者先把画面摆正.
func TestDecodeMirroredFrame(t *testing.T) {
	_, url := startTestServer(t)

	frame := mirroredFrame(t, qrFrame(t, "mirrored-code-42"))
	status, payload := postJSON(t, url, "/frame", frame)
	if status != http.StatusOK {
		t.Fatalf("状态码 = %d", status)
	}
	if len(payload.Codes) != 1 || payload.Codes[0].Text != "mirrored-code-42" {
		t.Fatalf("镜像帧结果 = %+v", payload.Codes)
	}
}

// TestRejectsInvalidFrame 验证非图片内容被明确拒绝.
func TestRejectsInvalidFrame(t *testing.T) {
	server, url := startTestServer(t)

	status, _ := postJSON(t, url, "/frame", []byte("not an image"))
	if status != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want %d", status, http.StatusBadRequest)
	}
	if got := server.Frames(); got != 0 {
		t.Fatalf("被拒绝的帧不应计数, Frames() = %d", got)
	}
}

// TestRejectsForeignSite 验证跨站请求被拒绝.
func TestRejectsForeignSite(t *testing.T) {
	_, url := startTestServer(t)

	req, err := http.NewRequest(http.MethodPost, url+"finish", nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /finish: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

// TestPageServed 验证首页返回 HTML 页面.
func TestPageServed(t *testing.T) {
	_, url := startTestServer(t)

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
		t.Fatalf("读取页面: %v", err)
	}
	if !bytes.Contains(body, []byte("getUserMedia")) {
		t.Fatal("页面应当使用 getUserMedia 打开摄像头")
	}
}
