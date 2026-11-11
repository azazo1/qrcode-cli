package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/azazo1/qrcode-cli/internal/source"
	"github.com/azazo1/qrcode-cli/internal/terminal"
)

// TestEncodeTerminalPaths 覆盖编码在终端下的三条分支.
func TestEncodeTerminalPaths(t *testing.T) {
	tty := true
	// 80x24 的典型终端, 用来构造"放得下"与"放不下"两种情况.
	size := [2]int{80, 24}

	short := fakeEnv(nil, nil, "", nil, true)
	longContent := strings.Repeat("a", 2000)

	envLong := fakeEnv(nil, nil, "", nil, true)

	cases := []struct {
		name      string
		args      []string
		env       source.Env
		wantHTML  bool
		wantChars bool
	}{
		{
			name:      "短文本画在终端",
			args:      []string{"e", "short-content"},
			env:       short,
			wantChars: true,
		},
		{
			name:     "长文本放不下转网页",
			args:     []string{"e", longContent, "--port", "0"},
			env:      envLong,
			wantHTML: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			// 网页模式下 Run 会阻塞到 ctx 取消, 因此先备好取消函数,
			// 在 Run 返回后立即取消, 避免测试挂住.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			r := &Runner{
				Stdout:           &out,
				Stderr:           &errb,
				StdinIsTerminal:  true,
				StdoutIsTerminal: &tty,
				TerminalSize:     &size,
				Env:              &c.env,
			}

			if c.wantHTML {
				// 网页分支会一直服务到取消; 用计时器在服务起来后取消.
				go func() {
					time.Sleep(300 * time.Millisecond)
					cancel()
				}()
			}

			code := r.Run(ctx, c.args)
			if code != exitOK {
				t.Fatalf("退出码 = %d (stderr: %s)", code, errb.String())
			}

			if c.wantChars {
				if !strings.Contains(out.String(), string(rune(0x2588))) &&
					!strings.Contains(out.String(), string(rune(0x2580))) {
					t.Fatalf("期望字符画输出, 实际:\n%s", out.String())
				}
				return
			}

			if c.wantHTML {
				// 网页模式下 stderr 应给出访问地址.
				if !strings.Contains(errb.String(), "http://127.0.0.1:") {
					t.Fatalf("期望输出网页地址, 实际 stderr: %s", errb.String())
				}
			}
		})
	}
}

// TestFitsBoundaryForLongContent 确认长内容确实会超过 80x24 终端.
func TestFitsBoundaryForLongContent(t *testing.T) {
	m := buildMatrixFor(t, strings.Repeat("a", 2000))
	if terminal.Fits(m, 80, 24) {
		t.Fatalf("长内容 (模块数 %d) 不应放进 80x24 终端", m.Size())
	}
}
