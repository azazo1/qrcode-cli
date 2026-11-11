package cli

import (
	"fmt"
	"strings"
)

// usage 是顶层帮助文本.
//
// 用法示例刻意采用项目 justfile 里的 recipe, 让读者从文档到命令的路径最短.
const usageTemplate = `qr - 二维码编解码工具

用法:
  qr [选项] [输入...]          自动判断输入是图片还是文本
  qr d|decode [选项] [输入...]  解码图片中的二维码
  qr e|encode [选项] [输入...]  把文本编码成二维码

自动判定规则:
  有位置参数且是图片文件       -> 解码
  有位置参数且是其他文件       -> 按文件内容编码
  有位置参数且不是文件         -> 按文本编码 (多个参数用空格拼接)
  无参数且标准输入是管道       -> 按魔数判断图片则解码, 否则编码
  无参数且标准输入是终端       -> 优先读剪贴板图片解码, 其次读剪贴板文本编码

解码选项:
%s
编码选项:
%s
通用选项:
%s
示例:
  qr e "https://example.com"     在终端显示二维码
  qr e "hello" > hello.png       输出 PNG 文件
  qr d hello.png                 解码图片文件
  cat shot.png | qr              从管道解码
  qr d shot.png                  解码含多个二维码的截图

退出码:
  0  成功
  1  用法错误, IO 错误或解码失败
  2  没有找到二维码
  130 被 Ctrl+C 中断
`

// usage 生成完整的帮助文本.
func usage(decodeFlags, encodeFlags, commonFlags string) string {
	return fmt.Sprintf(usageTemplate, decodeFlags, encodeFlags, commonFlags)
}

// normalizeCommand 把子命令别名统一成规范名.
//
// 支持 d/decode 与 e/encode 两种写法, 其余情况返回空串表示使用自动模式.
func normalizeCommand(arg string) string {
	switch strings.ToLower(arg) {
	case "d", "decode":
		return "decode"
	case "e", "encode":
		return "encode"
	default:
		return ""
	}
}
