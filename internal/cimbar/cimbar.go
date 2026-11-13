// Package cimbar 在本地起一个网页服务, 用浏览器页面完成 cimbar 格式的
// 大文件搬运: 发送页把文件编码成动态彩色图块, 接收页用摄像头读回, 还原出来的
// 文件由本服务保存到磁盘.
//
// 页面与编解码器是上游 libcimbar 的 WebAssembly 产物, 本包只负责静态资源分发
// 与结果落盘. 资源来源与许可说明见本目录的 README.md.
package cimbar

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/azazo1/qrcode-cli/internal/localweb"
	"github.com/azazo1/qrcode-cli/internal/pages"
)

//go:embed all:web
var webFS embed.FS

// webRoot 是内嵌资源的根目录.
var webRoot = mustSub()

func mustSub() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(fmt.Sprintf("切出内嵌页面目录失败: %v", err))
	}
	return sub
}

const (
	// maxUploadBytes 是单次上传的上限.
	//
	// cimbar 本身把单文件限制在压缩后 33MB 左右, 这里留出余量;
	// 上限必须存在, 否则异常请求能把内存撑爆.
	maxUploadBytes = 64 << 20

	// filenameHeader 携带经过 URL 编码的原始文件名.
	filenameHeader = "X-Filename"

	// landingPage 是入口页.
	landingPage = "landing.html"
)

// Options 是 cimbar 服务的选项.
type Options struct {
	// OutputDir 是还原出来的文件保存目录, 必填.
	OutputDir string
	// OnSave 在每个文件落盘后调用, 可以为空.
	OnSave func(Saved)
}

// Saved 是一次成功落盘的结果.
type Saved struct {
	// Name 是最终使用的文件名, 可能与发送方给的文件名不同 (重名时带序号).
	Name string
	// Path 是完整路径.
	Path string
	// Size 是文件字节数.
	Size int64
}

// Server 是 cimbar 的本地网页服务.
//
// 它不自己监听端口: 页面与上传接口通过 Mount 挂到宿主服务的 mux 上,
// 这样子命令可以单独启动它, 统一入口服务也能把它挂在 /cimbar 前缀下.
type Server struct {
	httpServer *http.Server
	outputDir  string
	onSave     func(Saved)
	mode       pages.Mode

	// saveMu 串行化"挑名字 + 改名"这一步, 避免并发上传互相覆盖.
	saveMu sync.Mutex

	mu    sync.Mutex
	saved []Saved
}

// New 创建一个 cimbar 服务, 此时还没有监听地址.
func New(opts Options) (*Server, error) {
	if opts.OutputDir == "" {
		return nil, errors.New("缺少输出目录")
	}
	outDir, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("解析输出目录失败: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建输出目录 %s 失败: %w", outDir, err)
	}
	return &Server{outputDir: outDir, onSave: opts.OnSave}, nil
}

// Mount 把页面, 静态资源与上传接口挂到 mux 上.
//
// prefix 形如 "" (服务本身就是页面根) 或 "/cimbar" (挂在统一入口服务的
// 子路径下). 页面里的引用都是相对路径, 因此两种挂载方式共用同一份资源.
func (s *Server) Mount(mux *http.ServeMux, prefix string, mode pages.Mode) {
	base := strings.TrimSuffix(prefix, "/")
	s.mode = mode
	pages.Mount(mux, base)
	mux.HandleFunc(base+"/upload", s.handleUpload)

	if base == "" {
		mux.HandleFunc("/", s.handlePage)
		return
	}
	mux.Handle(base+"/", http.StripPrefix(base, http.HandlerFunc(s.handlePage)))
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/", http.StatusFound)
	})
}

// Start 单独启动 cimbar 服务并返回可用的访问地址.
//
// addr 形如 "127.0.0.1:0", 端口为 0 时由系统分配; 返回的 URL 是入口页地址.
func Start(addr string, opts Options) (*Server, string, error) {
	s, err := New(opts)
	if err != nil {
		return nil, "", err
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("监听 %s 失败: %w", addr, err)
	}

	// 监听非回环地址时, 同一网络的设备也能打开页面并上传文件, 明确提醒一次.
	if !localweb.IsLoopback(listener.Addr()) {
		slog.Warn("网页服务监听在非回环地址, 同一网络的其他设备可以访问页面并上传文件", "addr", listener.Addr().String())
	}

	mux := http.NewServeMux()
	s.Mount(mux, "", pages.Solo)

	server := &http.Server{
		Handler:           mux,
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

// handlePage 提供入口页与静态资源.
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "只支持 GET 与 HEAD", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = landingPage
	}
	// 资源都是平铺部署的, 出现目录或向上跳转的路径一律当作不存在.
	if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		http.NotFound(w, r)
		return
	}

	data, err := fs.ReadFile(webRoot, name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Error("读取内嵌资源失败", "name", name, "error", err)
		}
		http.NotFound(w, r)
		return
	}

	// 入口页接入统一页头; 上游页面保留自己的布局, 只加一个悬浮返回入口.
	if name == landingPage {
		data = pages.Render(data, pages.Header(s.mode, "cimbar", "cimbar 传输"))
	} else if strings.HasSuffix(name, ".html") {
		data = pages.RenderReturn(data)
	}

	w.Header().Set("Content-Type", contentType(name))
	if name == "landing.html" || name == "recv.html" || name == "index.html" || name == "bridge.js" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		// 上游的文件名带构建时间戳, 内容变化时文件名也会变, 可以长期缓存.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// handleUpload 接收接收页还原出来的文件并落盘.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}

	// 浏览器发起的跨站 POST 会带 Origin, 与自身地址不一致的一律拒绝,
	// 避免任意网页往本地输出目录里写文件.
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			writeError(w, http.StatusForbidden, "拒绝跨站来源的写入")
			return
		}
	}

	rawName, err := url.PathUnescape(r.Header.Get(filenameHeader))
	if err != nil || strings.TrimSpace(rawName) == "" {
		writeError(w, http.StatusBadRequest, "缺少可用的文件名")
		return
	}
	name, err := sanitizeName(rawName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxUploadBytes)
	defer body.Close()

	tmp, err := os.CreateTemp(s.outputDir, ".cimbar-upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建临时文件失败")
		slog.Error("创建临时文件失败", "dir", s.outputDir, "error", err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	written, err := io.Copy(tmp, body)
	if err != nil {
		tmp.Close()
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "文件超过大小上限")
			return
		}
		writeError(w, http.StatusInternalServerError, "写入文件失败")
		slog.Error("写入临时文件失败", "path", tmpName, "error", err)
		return
	}
	if err := tmp.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "关闭临时文件失败")
		slog.Error("关闭临时文件失败", "path", tmpName, "error", err)
		return
	}

	s.saveMu.Lock()
	finalPath, err := s.uniquePath(name)
	if err == nil {
		err = os.Rename(tmpName, finalPath)
	}
	s.saveMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存文件失败")
		slog.Error("保存文件失败", "path", finalPath, "error", err)
		return
	}

	// 临时文件的权限是 0600, 改名后按普通文件放开, 免得用户还要自己 chmod.
	if err := os.Chmod(finalPath, 0o644); err != nil {
		slog.Warn("调整文件权限失败", "path", finalPath, "error", err)
	}

	saved := Saved{
		Name: filepath.Base(finalPath),
		Path: finalPath,
		Size: written,
	}
	s.mu.Lock()
	s.saved = append(s.saved, saved)
	s.mu.Unlock()

	slog.Info("cimbar 文件已保存", "name", saved.Name, "path", saved.Path, "bytes", saved.Size)
	if s.onSave != nil {
		s.onSave(saved)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name": saved.Name,
		"path": saved.Path,
		"size": saved.Size,
	})
}

// Saved 返回迄今为止落盘成功的文件, 用于退出时汇总.
func (s *Server) Saved() []Saved {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Saved, len(s.saved))
	copy(out, s.saved)
	return out
}

// Wait 阻塞直到 ctx 结束, 然后优雅关闭服务.
func (s *Server) Wait(ctx context.Context) error {
	<-ctx.Done()

	// 挂载到宿主服务时这里不持有监听, 没有需要关闭的东西.
	if s.httpServer == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 HTTP 服务失败: %w", err)
	}
	return nil
}

// uniquePath 在输出目录里挑一个不会覆盖已有文件的名字.
//
// 调用方必须持有 saveMu.
func (s *Server) uniquePath(name string) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := filepath.Join(s.outputDir, name)
	for i := 1; ; i++ {
		_, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		if i > 1000 {
			return "", errors.New("同名文件过多")
		}
		candidate = filepath.Join(s.outputDir, fmt.Sprintf("%s-%d%s", base, i, ext))
	}
}

// sanitizeName 把发送方给的文件名收敛成一个安全的纯文件名.
func sanitizeName(raw string) (string, error) {
	// 统一分隔符, 只取最后一段, 去掉任何目录成分.
	flat := strings.ReplaceAll(raw, `\`, "/")
	name := filepath.Base(filepath.Clean(flat))
	name = strings.TrimSpace(name)

	// 去掉控制字符与各平台文件名里的非法字符.
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		switch r {
		case '<', '>', ':', '"', '|', '?', '*':
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.TrimSuffix(name, "."))
	if name == "" || name == "." || name == ".." {
		return "", errors.New("文件名无效")
	}

	// 文件名过长时截断主体, 保留扩展名.
	const maxNameBytes = 200
	if len(name) > maxNameBytes {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		base := []rune(strings.TrimSuffix(name, ext))
		for len(string(base))+len(ext) > maxNameBytes && len(base) > 0 {
			base = base[:len(base)-1]
		}
		name = string(base) + ext
		if name == "" || name == "." || name == ".." {
			return "", errors.New("文件名无效")
		}
	}
	return name, nil
}

// contentType 按扩展名给出响应的类型.
//
// wasm 与 js 不能依赖系统 mime 表: 部分平台没有注册, 浏览器会拒绝实例化.
func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wasm":
		return "application/wasm"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	}
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("写出 JSON 响应失败", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
