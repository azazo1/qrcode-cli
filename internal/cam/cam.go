// Package cam 通过本地网页调起设备摄像头, 把画面帧交给解码器识别二维码.
//
// 摄像头采集交给浏览器完成: getUserMedia 在各桌面平台都有实现, 权限弹窗
// 与设备选择也由浏览器负责, 因此这里不需要为每个操作系统维护一套原生采集
// 代码. 本服务只做两件事: 提供页面, 以及把页面送来的帧交给解码器.
//
// 识别始终在 Go 侧进行, 与命令行的解码路径共用同一个 Decoder, 保证
// 预处理与识别能力完全一致. 页面不做任何图像变换 (包括镜像), 上传的就是
// 摄像头原始画面, 这样镜像二维码也能被正确识别.
package cam

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azazo1/qrcode-cli/internal/decode"
	"github.com/azazo1/qrcode-cli/internal/imageio"
	"github.com/azazo1/qrcode-cli/internal/localweb"
)

//go:embed index.html
var indexFS embed.FS

// maxFrameBytes 是单帧数据的上限.
//
// 页面会把画面缩到长边 1024 像素再压缩成 JPEG, 正常只有几十到一百多 KB.
// 上限留得宽松一些, 但必须存在, 避免异常的请求把内存撑爆.
const maxFrameBytes = 8 << 20

// Options 是摄像头扫码服务的选项.
type Options struct {
	// Decoder 负责识别帧里的二维码, 必填.
	Decoder decode.Decoder
}

// Hit 是一次被识别出来的二维码.
type Hit struct {
	Text    string `json:"text"`
	ECLevel string `json:"ec_level,omitempty"`
	// Width 与 Height 是二维码在帧里的像素尺寸, 仅用于页面展示.
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Server 是一个等待浏览器送来画面帧的本地服务.
type Server struct {
	httpServer *http.Server
	decoder    decode.Decoder

	// frameMu 让帧识别串行执行: 解码本身是 CPU 密集的, 并发处理多帧
	// 只会互相拖慢. 拿不到锁的帧直接丢弃, 页面会按节奏继续送下一帧.
	frameMu sync.Mutex

	// mu 保护 hits 与 seen.
	mu     sync.Mutex
	hits   []Hit
	seen   map[string]struct{}
	frames atomic.Int64

	// done 在用户结束或页面关闭时关闭.
	done chan struct{}
	once sync.Once

	serveErr error
}

// Start 在 addr 上启动服务并返回可访问的页面地址.
//
// addr 形如 "127.0.0.1:0", 端口为 0 时由系统分配. 服务会一直运行到
// 页面请求结束或 Wait 的 ctx 被取消.
func Start(addr string, opts Options) (*Server, string, error) {
	if opts.Decoder == nil {
		return nil, "", errors.New("缺少二维码解码器")
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("监听 %s 失败: %w", addr, err)
	}

	// 浏览器只在安全上下文里允许使用摄像头: 回环地址被视为安全上下文,
	// 其他地址需要 HTTPS. 监听非回环地址时提醒一次, 免得用户以为程序坏了.
	if !localweb.IsLoopback(listener.Addr()) {
		slog.Warn("网页服务监听在非回环地址, 浏览器通常只在 localhost 或 HTTPS 下允许使用摄像头",
			"addr", listener.Addr().String())
	}

	s := &Server{
		decoder: opts.Decoder,
		seen:    make(map[string]struct{}),
		done:    make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/frame", s.handleFrame)
	mux.HandleFunc("/hits", s.handleHits)
	mux.HandleFunc("/finish", s.handleFinish)
	mux.HandleFunc("/bye", s.handleFinish)

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.httpServer = server

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "error", err)
			s.mu.Lock()
			s.serveErr = err
			s.mu.Unlock()
			s.finish()
		}
	}()

	return s, localweb.BrowseURL(listener.Addr()), nil
}

// Hits 返回已识别到的二维码内容, 按首次识别的时间顺序排列, 内容去重.
func (s *Server) Hits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	texts := make([]string, len(s.hits))
	for i, hit := range s.hits {
		texts[i] = hit.Text
	}
	return texts
}

// Frames 返回已经识别过的帧数, 用于日志统计.
func (s *Server) Frames() int64 {
	return s.frames.Load()
}

// Wait 阻塞直到页面请求结束或 ctx 被取消, 然后关闭服务.
func (s *Server) Wait(ctx context.Context) error {
	select {
	case <-s.done:
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 HTTP 服务失败: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serveErr
}

// finish 通知 Wait 可以收尾了, 重复调用安全.
func (s *Server) finish() {
	s.once.Do(func() {
		close(s.done)
	})
}

// handleIndex 返回摄像头页面.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	page, err := indexFS.ReadFile("index.html")
	if err != nil {
		http.Error(w, "读取页面失败", http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(page); err != nil {
		slog.Debug("写入页面响应失败", "error", err)
	}
}

// handleFrame 接收一帧画面, 识别其中的二维码.
//
// 只把本次新出现的码返回给页面, 页面因此不需要自己维护去重逻辑.
// 上一帧还在识别时直接返回 202, 页面会按固定间隔继续送帧.
func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "拒绝来自其他站点的请求", http.StatusForbidden)
		return
	}

	if !s.frameMu.TryLock() {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	defer s.frameMu.Unlock()

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFrameBytes))
	if err != nil {
		slog.Debug("读取帧失败", "error", err)
		http.Error(w, "读取画面数据失败", http.StatusBadRequest)
		return
	}
	if len(data) == 0 {
		http.Error(w, "画面数据为空", http.StatusBadRequest)
		return
	}

	img, _, err := imageio.Decode(data)
	if err != nil {
		slog.Debug("解析画面失败", "bytes", len(data), "error", err)
		http.Error(w, "画面数据不是可识别的图片", http.StatusBadRequest)
		return
	}

	started := time.Now()
	results, err := s.decoder.Decode(r.Context(), img)
	elapsed := time.Since(started)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			slog.Debug("识别被中断")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		slog.Debug("识别画面失败", "error", err)
		http.Error(w, "识别画面失败", http.StatusInternalServerError)
		return
	}

	frames := s.frames.Add(1)
	fresh := s.record(results)
	slog.Debug("识别画面",
		"frames", frames,
		"codes", len(results),
		"new", len(fresh),
		"elapsed", elapsed)

	writeJSON(w, http.StatusOK, frameResponse{
		Codes:     fresh,
		ElapsedMS: elapsed.Milliseconds(),
	})
}

// record 把新识别到的内容并进结果集, 返回其中此前没有出现过的部分.
func (s *Server) record(results []decode.Result) []Hit {
	s.mu.Lock()
	defer s.mu.Unlock()

	fresh := make([]Hit, 0, len(results))
	for _, result := range results {
		if result.Text == "" {
			continue
		}
		if _, ok := s.seen[result.Text]; ok {
			continue
		}
		s.seen[result.Text] = struct{}{}

		hit := Hit{
			Text:    result.Text,
			ECLevel: result.ECLevel,
			Width:   result.Bounds.Width,
			Height:  result.Bounds.Height,
		}
		s.hits = append(s.hits, hit)
		fresh = append(fresh, hit)
	}
	return fresh
}

// handleHits 返回已经识别到的内容, 页面刷新后可以据此恢复列表.
func (s *Server) handleHits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只接受 GET 请求", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "拒绝来自其他站点的请求", http.StatusForbidden)
		return
	}

	s.mu.Lock()
	hits := make([]Hit, len(s.hits))
	copy(hits, s.hits)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, hitsResponse{Hits: hits})
}

// handleFinish 结束服务: 页面上点"结束"按钮, 或者页面被关闭时调用.
func (s *Server) handleFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "拒绝来自其他站点的请求", http.StatusForbidden)
		return
	}

	s.finish()
	w.WriteHeader(http.StatusNoContent)
}

// sameOrigin 只接受页面自身发出的请求.
//
// 浏览器对同源请求会标注 Sec-Fetch-Site; 其他网站对本服务的跨站请求一律
// 拒绝, 避免任意网页驱动本机做帧识别. 不带该头部的请求 (命令行工具,
// 老浏览器) 放行: 服务默认只监听回环地址, 本来就只有本机才能访问.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	default:
		return false
	}
}

// frameResponse 是 /frame 的响应.
type frameResponse struct {
	Codes     []Hit `json:"codes"`
	ElapsedMS int64 `json:"elapsed_ms"`
}

// hitsResponse 是 /hits 的响应.
type hitsResponse struct {
	Hits []Hit `json:"hits"`
}

// writeJSON 写出 JSON 响应.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Debug("写出 JSON 响应失败", "error", err)
	}
}
