package terminal

import (
	"strings"
	"testing"

	"github.com/azazo1/qrcode-cli/internal/encode"
)

// buildTestMatrix 构造一个已知内容的矩阵, 避免依赖真实编码结果的形状.
func buildTestMatrix(t *testing.T) *encode.Matrix {
	t.Helper()
	m, err := encode.Build("https://example.com", encode.LevelMedium)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

// TestRenderShape 校验渲染后的行数与每行字符数符合 "1 字符 = 1 列 x 2 行模块".
func TestRenderShape(t *testing.T) {
	m := buildTestMatrix(t)
	side := m.Size()

	cols, rows := Dimensions(m)
	if cols != side {
		t.Fatalf("cols = %d, want %d", cols, side)
	}
	if want := (side + 1) / 2; rows != want {
		t.Fatalf("rows = %d, want %d", rows, want)
	}

	out := Render(m, false)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != rows {
		t.Fatalf("输出行数 = %d, want %d", len(lines), rows)
	}

	allowed := map[rune]bool{blockFull: true, blockUpper: true, blockLower: true, blockEmpty: true}
	for i, line := range lines {
		runes := []rune(line)
		if len(runes) != side {
			t.Fatalf("第 %d 行字符数 = %d, want %d", i, len(runes), side)
		}
		for _, r := range runes {
			if !allowed[r] {
				t.Fatalf("第 %d 行出现意外字符 %q", i, r)
			}
		}
	}
}

// TestRenderColor 校验配色开关只影响 ANSI 转义序列, 不影响字符内容.
func TestRenderColor(t *testing.T) {
	m := buildTestMatrix(t)

	plain := Render(m, false)
	if strings.Contains(plain, "\x1b[") {
		t.Fatal("关闭配色时不应出现 ANSI 转义序列")
	}

	colored := Render(m, true)
	if !strings.Contains(colored, ansiPrefix) || !strings.Contains(colored, ansiReset) {
		t.Fatal("开启配色时应包含 ANSI 转义序列")
	}

	// 去掉转义序列后两者应完全一致.
	stripped := strings.ReplaceAll(colored, ansiPrefix, "")
	stripped = strings.ReplaceAll(stripped, ansiReset, "")
	if stripped != plain {
		t.Fatal("配色开关不应改变字符内容")
	}
}

// TestFits 校验放得下判定的边界.
func TestFits(t *testing.T) {
	m := buildTestMatrix(t)
	cols, rows := Dimensions(m)

	// 恰好够用 (含 2 列 2 行余量).
	if !Fits(m, cols+2, rows+2) {
		t.Fatal("刚好放得下时应返回 true")
	}
	// 宽度少一列就放不下.
	if Fits(m, cols+1, rows+2) {
		t.Fatal("宽度不足时应返回 false")
	}
	// 高度少一行同样放不下.
	if Fits(m, cols+2, rows+1) {
		t.Fatal("高度不足时应返回 false")
	}
}

// TestCell 校验四种模块组合到字符的映射.
func TestCell(t *testing.T) {
	cases := []struct {
		top, bottom bool
		want        rune
	}{
		{true, true, blockFull},
		{true, false, blockUpper},
		{false, true, blockLower},
		{false, false, blockEmpty},
	}
	for _, c := range cases {
		if got := cell(c.top, c.bottom); got != c.want {
			t.Fatalf("cell(%v,%v) = %q, want %q", c.top, c.bottom, got, c.want)
		}
	}
}
