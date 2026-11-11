// Package source 负责把用户给出的输入统一成"图片"或"文本".
//
// 输入可能来自位置参数, 标准输入管道或系统剪贴板. 这一层只做取数据与类型
// 判定, 不理解解码与编码的业务含义.
package source

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/azazo1/qrcode-cli/internal/clipboard"
	"github.com/azazo1/qrcode-cli/internal/imageio"
)

// Kind 是输入内容的种类.
type Kind int

const (
	// KindUnknown 表示还没有成功判定.
	KindUnknown Kind = iota
	// KindImage 表示是图片.
	KindImage
	// KindText 表示是文本.
	KindText
)

// String 返回可读的种类名.
func (k Kind) String() string {
	switch k {
	case KindImage:
		return "图片"
	case KindText:
		return "文本"
	default:
		return "未知"
	}
}

// Origin 描述输入从哪里来, 用于日志与出错提示.
type Origin string

const (
	OriginArg       Origin = "参数"
	OriginStdin     Origin = "管道"
	OriginClipboard Origin = "剪贴板"
	OriginFile      Origin = "文件"
)

// Code 返回用于机器可读输出 (例如 JSON) 的稳定英文标识.
//
// 人类可读输出使用 Origin 本身的中文名, 而结构化输出必须与界面语言无关,
// 否则使用方的解析逻辑会被界面调整破坏.
func (o Origin) Code() string {
	switch o {
	case OriginArg:
		return "arg"
	case OriginStdin:
		return "stdin"
	case OriginClipboard:
		return "clipboard"
	case OriginFile:
		return "file"
	default:
		return "unknown"
	}
}

// Input 是从某个来源取得的内容.
type Input struct {
	// Origin 说明数据来自何处.
	Origin Origin
	// Label 是展示用的来源名称, 例如文件名.
	Label string
	// Kind 是判定出的内容种类.
	Kind Kind
	// Data 是原始字节, 图片时是图片文件内容, 文本时是文本字节.
	Data []byte
	// Format 是图片格式, 仅 KindImage 时有效.
	Format imageio.Format
}

// 判定所需的依赖, 抽成结构体便于测试注入.
type Env struct {
	// Stdin 是标准输入.
	Stdin io.Reader
	// StdinIsTerminal 报告标准输入是否连接到终端.
	// 为 true 时说明没有管道输入.
	StdinIsTerminal bool
	// ReadClipboardImage 读取剪贴板图片, 没有图片时返回 clipboard.ErrImageUnavailable.
	ReadClipboardImage func() ([]byte, error)
	// ReadClipboardText 读取剪贴板文本.
	ReadClipboardText func() ([]byte, error)
	// ReadFile 读取文件, 默认使用 os.ReadFile.
	ReadFile func(path string) ([]byte, error)
	// Stat 判断路径是否存在以及是否为目录.
	Stat func(path string) (os.FileInfo, error)
}

// DefaultEnv 返回接入真实系统环境的依赖集合.
func DefaultEnv(stdinIsTerminal bool) Env {
	return Env{
		Stdin:              os.Stdin,
		StdinIsTerminal:    stdinIsTerminal,
		ReadClipboardImage: clipboard.Image,
		ReadClipboardText:  clipboard.Text,
		ReadFile:           os.ReadFile,
		Stat:               os.Stat,
	}
}

// ErrNoInput 表示没有任何可用输入.
var ErrNoInput = errors.New("没有输入内容, 也没有可用的剪贴板数据")

// Classify 对一段字节判断它是图片还是文本.
// 依据是文件头魔数, 这与扩展名和文件类型无关.
func Classify(data []byte) (Kind, imageio.Format) {
	if format, ok := imageio.SniffFormat(data); ok {
		return KindImage, format
	}
	return KindText, ""
}

// FromArgs 处理命令行位置参数.
//
// 规则: 第一个参数若是存在的文件, 则读文件内容并按魔数判定; 否则把所有参数
// 视为文本, 用空格拼接. "-" 表示读取标准输入.
func (e Env) FromArgs(args []string) (Input, error) {
	if len(args) == 0 {
		return Input{}, errors.New("没有位置参数")
	}

	if args[0] == "-" {
		return e.FromStdin()
	}

	// 只有单个参数且它确实是存在的普通文件时才读文件.
	// 多个参数一律视为文本, 避免把 "qr e a b" 误当成两个文件.
	if len(args) == 1 {
		if info, err := e.Stat(args[0]); err == nil && !info.IsDir() {
			data, err := e.ReadFile(args[0])
			if err != nil {
				return Input{}, fmt.Errorf("读取文件 %s 失败: %w", args[0], err)
			}
			kind, format := Classify(data)
			if kind == KindText {
				// 文本文件几乎都以换行结尾, 那个换行通常不是要编码的内容.
				data = TrimTrailingNewline(data)
			}
			return Input{
				Origin: OriginFile,
				Label:  args[0],
				Kind:   kind,
				Data:   data,
				Format: format,
			}, nil
		}
	}

	// 显式写在命令行上的文本保持原样, 这是需要编码换行时的出口.
	text := joinArgs(args)
	return Input{
		Origin: OriginArg,
		Label:  "参数",
		Kind:   KindText,
		Data:   []byte(text),
	}, nil
}

// joinArgs 用单个空格连接多个参数.
func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}

// FromStdin 读取标准输入并按魔数判定.
func (e Env) FromStdin() (Input, error) {
	if e.Stdin == nil {
		return Input{}, errors.New("标准输入不可用")
	}

	data, err := io.ReadAll(e.Stdin)
	if err != nil {
		return Input{}, fmt.Errorf("读取标准输入失败: %w", err)
	}
	if len(data) == 0 {
		return Input{}, ErrNoInput
	}

	kind, format := Classify(data)
	if kind == KindText {
		data = TrimTrailingNewline(data)
	}
	return Input{
		Origin: OriginStdin,
		Label:  "标准输入",
		Kind:   kind,
		Data:   data,
		Format: format,
	}, nil
}

// FromClipboardImage 读取剪贴板图片.
// 剪贴板没有图片时返回 clipboard.ErrImageUnavailable, 调用方可据此回退.
func (e Env) FromClipboardImage() (Input, error) {
	data, err := e.ReadClipboardImage()
	if err != nil {
		return Input{}, err
	}

	kind, format := Classify(data)
	if kind != KindImage {
		return Input{}, clipboard.ErrImageUnavailable
	}
	return Input{
		Origin: OriginClipboard,
		Label:  "剪贴板图片",
		Kind:   KindImage,
		Data:   data,
		Format: format,
	}, nil
}

// FromClipboardText 读取剪贴板文本.
func (e Env) FromClipboardText() (Input, error) {
	data, err := e.ReadClipboardText()
	if err != nil {
		return Input{}, err
	}
	if len(data) == 0 {
		return Input{}, ErrNoInput
	}
	return Input{
		Origin: OriginClipboard,
		Label:  "剪贴板文本",
		Kind:   KindText,
		Data:   TrimTrailingNewline(data),
	}, nil
}

// TrimTrailingNewline 去掉文本末尾的单个换行.
//
// 从管道, 文件或剪贴板取到的文本几乎总以换行结束, 例如 echo 会补一个,
// 编辑器保存文件也会补一个. 那个换行通常不是用户要编码的内容, 留在二维码里
// 会让扫描结果多出一个看不见的空行. 只去一个, 且去完为空时保留原样,
// 避免把纯换行输入变成空内容.
func TrimTrailingNewline(data []byte) []byte {
	trimmed := data
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\n' {
		trimmed = trimmed[:len(trimmed)-1]
		// Windows 风格的 CRLF 一并处理.
		if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\r' {
			trimmed = trimmed[:len(trimmed)-1]
		}
	}
	if len(trimmed) == 0 {
		return data
	}
	return trimmed
}

// Auto 实现无子命令时的自动判定.
//
// 顺序: 位置参数 > 非终端的标准输入 > 剪贴板.
//
// 标准输入读到空内容时不算输入, 继续尝试剪贴板: 非交互式环境 (脚本, IDE 内置
// 终端, 部分 CI) 常常把 stdin 接到 /dev/null 而不是终端, 这时如果剪贴板里明明
// 有内容却报"没有输入", 会让人以为是工具坏了.
//
// 剪贴板优先取图片, 没有再取文本.
func (e Env) Auto(args []string) (Input, error) {
	if len(args) > 0 {
		return e.FromArgs(args)
	}

	if !e.StdinIsTerminal {
		input, err := e.FromStdin()
		if err == nil {
			return input, nil
		}
		// 只有"确实没有数据"才继续往下走; 真正的读取故障应当直接报错.
		if !errors.Is(err, ErrNoInput) {
			return Input{}, err
		}
	}

	return e.fromClipboard()
}

// fromClipboard 依次尝试剪贴板图片与文本.
func (e Env) fromClipboard() (Input, error) {
	if input, err := e.FromClipboardImage(); err == nil {
		return input, nil
	} else if !errors.Is(err, clipboard.ErrImageUnavailable) && !errors.Is(err, clipboard.ErrUnsupported) {
		return Input{}, err
	}

	if input, err := e.FromClipboardText(); err == nil {
		return input, nil
	} else if !errors.Is(err, ErrNoInput) && !errors.Is(err, clipboard.ErrUnsupported) {
		return Input{}, err
	}

	return Input{}, ErrNoInput
}
