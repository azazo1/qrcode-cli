// Package localweb 提供本地网页服务共用的监听地址处理.
//
// 启动网页服务后要打印一个能直接点开的地址, 而监听地址本身不总是可用的地址:
// 监听 0.0.0.0 或 :: 时真实地址形如 http://[::]:8080, 浏览器打不开.
package localweb

import (
	"net"
	"strconv"
)

// BrowseURL 把监听地址转换成可以直接在浏览器打开的地址.
//
// 监听未指定地址 (0.0.0.0 或 ::) 时改用回环地址, 指定了具体网卡地址时沿用.
func BrowseURL(addr net.Addr) string {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return "http://" + addr.String() + "/"
	}

	host := tcpAddr.IP.String()
	if tcpAddr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(tcpAddr.Port)) + "/"
}

// IsLoopback 报告监听地址是否只接受本机连接.
func IsLoopback(addr net.Addr) bool {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	return tcpAddr.IP.IsLoopback()
}
