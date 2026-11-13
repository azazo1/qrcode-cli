// Package home 提供统一入口服务: 主菜单, 编码页, 解码页, 摄像头扫码页与
// cimbar 传输页都挂在同一个本地服务上.
//
// 摄像头与 cimbar 各自的子命令 (qr cam, qr cimbar) 仍然存在, 它们启动的
// 就是这里挂载的同一套页面与接口, 只是挂在根路径下.
package home

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/azazo1/qrcode-cli/internal/cam"
	"github.com/azazo1/qrcode-cli/internal/cimbar"
	"github.com/azazo1/qrcode-cli/internal/decode"
	"github.com/azazo1/qrcode-cli/internal/encode"
	"github.com/azazo1/qrcode-cli/internal/imageio"
	"github.com/azazo1/qrcode-cli/internal/localweb"
	"github.com/azazo1/qrcode-cli/internal/pages"
)

//go:embed home.html encode.html decode.html
var pageFS embed.FS

const (
	// maxEncodeBytes 是编码接口接受的文本上限.
	// 二维码能装下的内容远小于这个值, 这里只是防止异常的请求体.
	maxEncodeBytes = 8 << 10

	// maxDecodeBytes 是解码接口接受的图片上限.
	maxDecodeBytes = 16 << 20

	// renderModuleSize 是编码页展示二维码时每个模块的像素数.
	renderModuleSize = 8
)

// Options 是入口服务的选项.
type Options struct {
	// OutputDir 是 cimbar 接收文件的保存目录, 必填.
	OutputDir string
	// OnSave 在 cimbar 文件落盘后调用, 可以为空.
	OnSave func(cimbar.Saved)
}

// Server 是统一入口服务.
type Server struct {
	httpServer *http.Server
	cam        *cam.Server
	cimbar     *cimbar.Server
	decoder    decode.Decoder
}

// New 创建入口服务, 此时还没有监听地址.
func New(opts Options) (*Server, error) {
	camServer, err := cam.New(cam.Options{Decoder: decode.New(decode.Options{})})
	if err != nil {
		return nil, err
	}
	cimbarServer, err := cimbar.New(cimbar.Options{OutputDir: opts.OutputDir, OnSave: opts.OnSave})
	if err != nil {
		return nil, err
	}
	return &Server{
		cam:     camServer,
		cimbar:  cimbarServer,
		decoder: decode.New(decode.Options{}),
	}, nil
}

// Handler 组装全部路由.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	pages.Mount(mux, "")

	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/encode", s.handleEncodePage)
	mux.HandleFunc("/decode", s.handleDecodePage)
	mux.HandleFunc("/api/encode", s.handleEncode)
	mux.HandleFunc("/api/decode", s.handleDecode)

	s.cam.Mount(mux, "/cam", pages.Home)
	s.cimbar.Mount(mux, "/cimbar", pages.Home)
	return mux
}

// Cam 返回摄像头扫码的状态, 用于退出时汇总识别结果.
func (s *Server) Cam() *cam.Server {
	return s.cam
}

// Cimbar 返回 cimbar 的状态, 用于退出时汇总已保存的文件.
func (s *Server) Cimbar() *cimbar.Server {
	return s.cimbar
}

// Start 在 addr 上启动入口服务并返回访问地址.
//
// addr 形如 "127.0.0.1:0", 端口为 0 时由系统分配.
func Start(addr string, opts Options) (*Server, string, error) {
	s, err := New(opts)
	if err != nil {
		return nil, "", err
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("监听 %s 失败: %w", addr, err)
	}

	// 浏览器只在安全上下文里允许使用摄像头: 回环地址被视为安全上下文,
	// 其他地址需要 HTTPS. 监听非回环地址时提醒一次.
	if !localweb.IsLoopback(listener.Addr()) {
		slog.Warn("网页服务监听在非回环地址, 浏览器通常只在 localhost 或 HTTPS 下允许使用摄像头",
			"addr", listener.Addr().String())
	}

	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.httpServer = server

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

// handleHome 返回主菜单.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.writePage(w, "home.html", "", "")
}

// handleEncodePage 返回编码页面.
func (s *Server) handleEncodePage(w http.ResponseWriter, r *http.Request) {
	s.writePage(w, "encode.html", "encode", "编码")
}

// handleDecodePage 返回解码页面.
func (s *Server) handleDecodePage(w http.ResponseWriter, r *http.Request) {
	s.writePage(w, "decode.html", "decode", "解码")
}

// writePage 读取内嵌页面, 换上统一页头后写出.
func (s *Server) writePage(w http.ResponseWriter, name, active, title string) {
	data, err := pageFS.ReadFile(name)
	if err != nil {
		slog.Error("读取内嵌页面失败", "name", name, "error", err)
		http.Error(w, "读取页面失败", http.StatusInternalServerError)
		return
	}
	page := pages.Render(data, pages.Header(pages.Home, active, title))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write(page); err != nil {
		slog.Debug("写入页面响应失败", "error", err)
	}
}

// encodeResponse 是编码接口的响应.
type encodeResponse struct {
	Content string `json:"content"`
	Version int    `json:"version"`
	Level   string `json:"level"`
	Modules int    `json:"modules"`
	DataURI string `json:"data_uri"`
}

// handleEncode 把请求体里的文本编码成二维码 PNG.
func (s *Server) handleEncode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEncodeBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "内容超过大小上限")
			return
		}
		writeError(w, http.StatusBadRequest, "读取请求失败")
		return
	}

	text := string(body)
	if strings.TrimSpace(text) == "" {
		writeError(w, http.StatusBadRequest, "内容为空")
		return
	}

	matrix, err := encode.Build(text, encode.LevelMedium)
	if err != nil {
		slog.Debug("编码失败", "error", err)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("内容放不进二维码: %v", err))
		return
	}
	pngData, err := matrix.RenderPNG(renderModuleSize)
	if err != nil {
		slog.Error("渲染二维码失败", "error", err)
		writeError(w, http.StatusInternalServerError, "生成图片失败")
		return
	}

	writeJSON(w, http.StatusOK, encodeResponse{
		Content: matrix.Content,
		Version: matrix.Version,
		Level:   string(matrix.Level),
		Modules: matrix.Size(),
		DataURI: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData),
	})
}

// decodeResponse 是解码接口的响应.
type decodeResponse struct {
	Results []decode.Result `json:"results"`
}

// handleDecode 识别请求体里图片中的二维码.
func (s *Server) handleDecode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDecodeBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "图片超过大小上限")
			return
		}
		writeError(w, http.StatusBadRequest, "读取请求失败")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "没有收到图片数据")
		return
	}

	img, _, err := imageio.Decode(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "不是可识别的图片")
		return
	}

	results, err := s.decoder.Decode(r.Context(), img)
	if err != nil {
		slog.Error("识别图片失败", "error", err)
		writeError(w, http.StatusInternalServerError, "识别失败")
		return
	}
	if results == nil {
		results = []decode.Result{}
	}
	slog.Debug("解码页面识别完成", "codes", len(results))
	writeJSON(w, http.StatusOK, decodeResponse{Results: results})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Debug("写出 JSON 响应失败", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
