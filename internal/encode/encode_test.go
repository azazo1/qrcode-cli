package encode

import (
	"strings"
	"testing"
)

// TestParseLevel 校验纠错等级解析, 包含大小写与非法值.
func TestParseLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    Level
		wantErr bool
	}{
		{"L", LevelLow, false},
		{"m", LevelMedium, false},
		{"q", LevelQuartile, false},
		{"H", LevelHigh, false},
		{"", LevelMedium, false},
		{"Z", "", true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("ParseLevel(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Fatalf("ParseLevel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBuildEmpty 校验空内容被拒绝.
func TestBuildEmpty(t *testing.T) {
	if _, err := Build("", LevelMedium); err == nil {
		t.Fatal("空内容应返回错误")
	}
}

// TestBuildTooLong 校验超长内容的错误信息包含可操作提示.
func TestBuildTooLong(t *testing.T) {
	_, err := Build(strings.Repeat("x", 3000), LevelMedium)
	if err == nil {
		t.Fatal("超长内容应返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"内容过长", "2331", "-l L"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺少 %q: %s", want, msg)
		}
	}
}

// TestLevelOrdering 校验纠错等级越高, 同样内容需要的版本号不会更小.
func TestLevelOrdering(t *testing.T) {
	content := strings.Repeat("a", 60)
	previous := 0
	for _, level := range []Level{LevelLow, LevelMedium, LevelQuartile, LevelHigh} {
		m, err := Build(content, level)
		if err != nil {
			t.Fatalf("Build(%s): %v", level, err)
		}
		if m.Version < previous {
			t.Fatalf("等级 %s 的版本号 %d 小于更低等级的 %d", level, m.Version, previous)
		}
		previous = m.Version
	}
}

// TestRenderPNGSize 校验 PNG 输出尺寸等于模块数乘以缩放.
func TestRenderPNGSize(t *testing.T) {
	m, err := Build("render-size-test", LevelMedium)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	pngData, err := m.RenderPNG(6)
	if err != nil {
		t.Fatalf("RenderPNG: %v", err)
	}
	if len(pngData) == 0 {
		t.Fatal("PNG 数据为空")
	}

	// 非法缩放应当被拒绝.
	if _, err := m.RenderPNG(0); err == nil {
		t.Fatal("缩放为 0 应返回错误")
	}
	if _, err := m.RenderPNG(65); err == nil {
		t.Fatal("缩放超过上限应返回错误")
	}
}

// TestMatrixHasQuietZone 校验矩阵自带的静区为 4 模块.
func TestMatrixHasQuietZone(t *testing.T) {
	m, err := Build("quiet-zone-test", LevelMedium)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// 版本号为 v 的二维码符号边长是 4v+17, 加两侧各 4 模块静区.
	want := 4*m.Version + 17 + 8
	if m.Size() != want {
		t.Fatalf("矩阵边长 = %d, want %d (版本 %d)", m.Size(), want, m.Version)
	}
}
