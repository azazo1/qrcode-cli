package cimbar

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startTestServer 起一个监听随机端口的服务, 测试结束自动关闭.
func startTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()

	dir := t.TempDir()
	server, base, err := Start("127.0.0.1:0", Options{OutputDir: dir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = server.Wait(ctx)
	})
	return server, base, dir
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

// TestLandingAndAssets 确认入口页与上游页面都能正常取到, 且 wasm 的类型正确.
func TestLandingAndAssets(t *testing.T) {
	_, base, _ := startTestServer(t)

	code, _, body := get(t, base)
	if code != http.StatusOK {
		t.Fatalf("入口页状态码 = %d", code)
	}
	if !strings.Contains(body, "cimbar") {
		t.Fatalf("入口页内容不含项目名")
	}

	if code, _, body = get(t, base+"index.html"); code != http.StatusOK || !strings.Contains(body, "canvas") {
		t.Fatalf("发送页不可用: 状态码 %d", code)
	}

	if code, _, body = get(t, base+"recv.html"); code != http.StatusOK || !strings.Contains(body, "bridge.js") {
		t.Fatalf("接收页没有加载桥接脚本: 状态码 %d", code)
	}

	names, err := fs.Glob(webRoot, "*.wasm")
	if err != nil || len(names) != 1 {
		t.Fatalf("内嵌资源里应当只有一个 wasm: %v %v", names, err)
	}
	code, header, _ := get(t, base+names[0])
	if code != http.StatusOK {
		t.Fatalf("wasm 状态码 = %d", code)
	}
	if got := header.Get("Content-Type"); got != "application/wasm" {
		t.Fatalf("wasm 的类型 = %q", got)
	}

	if code, _, _ = get(t, base+"不存在的文件"); code != http.StatusNotFound {
		t.Fatalf("不存在的资源状态码 = %d, 期望 404", code)
	}
}

// TestUploadSavesFile 确认接收页上传的内容会写到输出目录.
func TestUploadSavesFile(t *testing.T) {
	_, base, dir := startTestServer(t)

	content := []byte("cimbar 传输的内容\n")
	req, err := http.NewRequest(http.MethodPost, base+"upload", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set(filenameHeader, url.PathEscape("报告 2026.pdf"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("返回的大小 = %d", result.Size)
	}
	if result.Name != "报告 2026.pdf" {
		t.Fatalf("返回的文件名 = %q", result.Name)
	}
	if filepath.Dir(result.Path) != dir {
		t.Fatalf("文件没有落在输出目录: %s", result.Path)
	}

	saved, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("读取落盘文件: %v", err)
	}
	if !bytes.Equal(saved, content) {
		t.Fatalf("落盘内容与上传内容不一致")
	}
}

// TestUploadSanitizesName 确认带目录成分的文件名不会逃出输出目录.
func TestUploadSanitizesName(t *testing.T) {
	_, base, dir := startTestServer(t)

	req, err := http.NewRequest(http.MethodPost, base+"upload", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set(filenameHeader, url.PathEscape("../../evil.txt"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("上传状态码 = %d", resp.StatusCode)
	}

	got := filepath.Join(dir, "evil.txt")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("期望落在 %s: %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
		t.Fatalf("文件逃出了输出目录")
	}
}

// TestUploadKeepsExistingFile 确认同名文件不会互相覆盖.
func TestUploadKeepsExistingFile(t *testing.T) {
	_, base, dir := startTestServer(t)

	for _, content := range []string{"第一个", "第二个"} {
		req, err := http.NewRequest(http.MethodPost, base+"upload", strings.NewReader(content))
		if err != nil {
			t.Fatalf("构造请求: %v", err)
		}
		req.Header.Set(filenameHeader, url.PathEscape("note.txt"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST upload: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("上传状态码 = %d", resp.StatusCode)
		}
	}

	first, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil || string(first) != "第一个" {
		t.Fatalf("第一个文件内容 = %q, err = %v", first, err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "note-1.txt"))
	if err != nil || string(second) != "第二个" {
		t.Fatalf("第二个文件内容 = %q, err = %v", second, err)
	}
}

// TestUploadRejectsCrossOrigin 确认别的网页不能借浏览器往输出目录写文件.
func TestUploadRejectsCrossOrigin(t *testing.T) {
	_, base, dir := startTestServer(t)

	req, err := http.NewRequest(http.MethodPost, base+"upload", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set(filenameHeader, url.PathEscape("cross.txt"))
	req.Header.Set("Origin", "https://example.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("跨站上传状态码 = %d, 期望 403", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "cross.txt")); err == nil {
		t.Fatalf("跨站请求写出了文件")
	}
}

// TestSanitizeName 覆盖文件名的边界情况.
func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "hello.txt", want: "hello.txt"},
		{in: "a/b/c.png", want: "c.png"},
		{in: `C:\temp\x.zip`, want: "x.zip"},
		{in: "..", wantErr: true},
		{in: "", wantErr: true},
		{in: "a\x00b.txt", want: "ab.txt"},
		{in: `bad:name?.txt`, want: "bad_name_.txt"},
	}
	for _, c := range cases {
		got, err := sanitizeName(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("sanitizeName(%q) 期望失败, 得到 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("sanitizeName(%q) 失败: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("sanitizeName(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
