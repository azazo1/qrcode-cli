// Package browser 用系统默认浏览器打开一个地址.
//
// 命令行工具在把内容交给浏览器查看时需要一个统一的入口: 各平台的启动方式不同,
// 但都只需要一次短命令调用, 没有引入系统库绑定的必要.
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open 用系统默认浏览器打开 url.
//
// 返回的 error 只表示"没能把打开请求发出去", 不代表浏览器真的显示了页面;
// 调用方应当把失败当作提示信息处理, 而不是中断正在进行的其它工作.
func Open(url string) error {
	name, args, err := command(url)
	if err != nil {
		return err
	}

	if err := exec.Command(name, args...).Start(); err != nil {
		return fmt.Errorf("调用 %s 打开浏览器失败: %w", name, err)
	}
	return nil
}

// command 按当前平台给出打开地址的命令与参数.
func command(url string) (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}, nil
	case "windows":
		// rundll32 比 "cmd /c start" 更稳: start 是 cmd 的内建命令,
		// 而 url 里的 & 会被 cmd 当成命令分隔符.
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{url}, nil
	default:
		return "", nil, fmt.Errorf("当前平台 %s 不支持自动打开浏览器", runtime.GOOS)
	}
}
