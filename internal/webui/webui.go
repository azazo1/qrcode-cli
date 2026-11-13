// Package webui 在终端放不下二维码时, 起一个本地 HTTP 服务把完整二维码
// 显示在浏览器里.
//
// 页面只服务本机: 默认只监听回环地址, 并且服务一直运行到用户按 Ctrl+C.
package webui

import (
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/azazo1/qrcode-cli/internal/encode"
	"github.com/azazo1/qrcode-cli/internal/localweb"
	"github.com/azazo1/qrcode-cli/internal/pages"
)

//go:embed page.html
var pageFS embed.FS

// pageData 是模板需要的数据.
type pageData struct {
	// Content 是被编码的原始文本, 页面上供复制.
	Content string
	// QRDataURI 是内联的 PNG 数据, 避免额外请求与缓存问题.
	//
	// 类型写成 template.URL 而不是 string: html/template 会对 src 属性做
	// URL 转义, base64 里的 +, /, = 会被转成实体, 图片随之失效.
	// 该值由本程序生成的 PNG 与固定前缀拼成, 不含用户可控内容.
	QRDataURI template.URL
	// Modules 是二维码矩阵边长 (含静区).
	Modules int
	// Version 是二维码版本号.
	Version int
	// Level 是纠错等级.
	Level string
	// Nav 是统一页头, 由 pages 包生成.
	Nav template.HTML
}

// Server 是一个展示二维码的本地 HTTP 服务.
type Server struct {
	httpServer *http.Server
}

// Start 在 addr 上启动服务并返回可用的访问地址.
//
// addr 形如 "127.0.0.1:0", 端口为 0 时由系统分配; 返回的 URL 已经是
// 实际监听端口拼好的完整地址.
func Start(addr string, m *encode.Matrix, pngData []byte) (*Server, string, error) {
	tmpl, err := template.ParseFS(pageFS, "page.html")
	if err != nil {
		return nil, "", fmt.Errorf("解析页面模板失败: %w", err)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("监听 %s 失败: %w", addr, err)
	}

	// 监听非回环地址时二维码内容对内网可见, 明确提醒一次.
	if !localweb.IsLoopback(listener.Addr()) {
		slog.Warn("网页服务监听在非回环地址, 同一网络的其他设备可以访问二维码内容", "addr", listener.Addr().String())
	}

	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)
	data := pageData{
		Content:   m.Content,
		QRDataURI: template.URL(uri),
		Modules:   m.Size(),
		Version:   m.Version,
		Level:     string(m.Level),
		Nav:       template.HTML(pages.Header(pages.Solo, "encode", "二维码")),
	}

	mux := http.NewServeMux()
	pages.Mount(mux, "")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			slog.Error("渲染页面失败", "error", err)
		}
	})
	mux.HandleFunc("/qr.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(pngData); err != nil {
			slog.Debug("写入 PNG 响应失败", "error", err)
		}
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s := &Server{httpServer: server}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "error", err)
		}
	}()

	return s, localweb.BrowseURL(listener.Addr()), nil
}

// Wait 阻塞直到 ctx 结束, 然后优雅关闭服务.
func (s *Server) Wait(ctx context.Context) error {
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 HTTP 服务失败: %w", err)
	}
	return nil
}
