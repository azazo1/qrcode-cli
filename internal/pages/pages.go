// Package pages 提供本地网页共用的样式, 页头与静态资源挂载.
//
// 各子命令 (编码网页, 摄像头扫码, cimbar 传输) 与统一入口服务都从这里取页头,
// 因此所有页面共用同一套导航与外观. 页面输出保持纯静态 HTML, 只有页头占位符
// 在服务端被替换, 不需要模板引擎.
package pages

import (
	"bytes"
	"embed"
	"html"
	"net/http"
	"strings"
)

//go:embed style.css
var assets embed.FS

// NavPlaceholder 是页面 HTML 里标记页头位置的注释.
//
// 页面里没有这个占位符时 Render 原样返回, 便于上游页面按需接入.
const NavPlaceholder = "<!--NAV-->"

// Mode 决定页头展示哪些入口.
type Mode int

const (
	// Solo 用于子命令单独启动的服务, 页头只有品牌与页面名.
	Solo Mode = iota
	// Home 用于统一入口服务, 页头带编码, 解码, 摄像头与 cimbar 四个入口.
	Home
)

// navItem 是导航条上的一项.
type navItem struct {
	path   string
	label  string
	active string
}

// navItems 是统一入口服务里的功能入口.
var navItems = []navItem{
	{path: "/encode", label: "编码", active: "encode"},
	{path: "/decode", label: "解码", active: "decode"},
	{path: "/cam/", label: "摄像头", active: "cam"},
	{path: "/cimbar/", label: "cimbar", active: "cimbar"},
}

// Mount 在 mux 上挂载共享资源, 地址是 prefix + "assets/style.css".
//
// prefix 形如 "" (服务本身就是页面根) 或 "/cam" (挂在统一入口服务的子路径下).
func Mount(mux *http.ServeMux, prefix string) {
	base := strings.TrimSuffix(prefix, "/")
	mux.HandleFunc(base+"/assets/style.css", handleStyle)
}

func handleStyle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "只支持 GET 与 HEAD", http.StatusMethodNotAllowed)
		return
	}
	data, err := assets.ReadFile("style.css")
	if err != nil {
		http.Error(w, "读取样式失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write(data); err != nil {
		return
	}
}

// Header 生成统一页头.
//
// active 是当前功能标识 (encode, decode, cam, cimbar), 用于高亮导航项;
// title 是页面名, 单独启动时它是页头里唯一的说明.
func Header(mode Mode, active, title string) string {
	var b strings.Builder
	b.WriteString(`<header class="app-bar">`)
	if mode == Home {
		b.WriteString(`<a class="app-brand" href="/">qr</a>`)
	} else {
		b.WriteString(`<span class="app-brand">qr</span>`)
	}
	if title != "" {
		b.WriteString(`<span class="app-page">` + html.EscapeString(title) + `</span>`)
	}
	if mode == Home {
		b.WriteString(`<nav class="app-nav">`)
		for _, item := range navItems {
			class := ""
			if item.active == active {
				class = ` class="active"`
			}
			b.WriteString(`<a href="` + item.path + `"` + class + `>` + item.label + `</a>`)
		}
		b.WriteString(`</nav>`)
	}
	b.WriteString(`</header>`)
	return b.String()
}

// ReturnLink 是给没有完整页头的页面 (cimbar 的上游页面) 用的悬浮返回入口.
func ReturnLink() string {
	return `<a class="btn cimbar-return" href="/">返回</a>`
}

// ReturnPlaceholder 是上游页面里返回入口的占位符.
const ReturnPlaceholder = "<!--RETURN-->"

// RenderReturn 把上游页面里的返回入口占位符换成悬浮返回链接.
func RenderReturn(page []byte) []byte {
	return bytes.ReplaceAll(page, []byte(ReturnPlaceholder), []byte(ReturnLink()))
}

// Render 把页面里的页头占位符替换成 header.
func Render(page []byte, header string) []byte {
	return bytes.ReplaceAll(page, []byte(NavPlaceholder), []byte(header))
}
