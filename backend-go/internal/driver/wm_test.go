package driver

import (
	"os"
	"testing"
)

// 水印是打卡照片的证据属性所在：没字体就静默不水印，等于悄悄丢掉证据，
// 因此这条测试守的是「本机确实能渲染中文水印」。
func TestWatermarkRenders(t *testing.T) {
	if loadCJKFont() == nil {
		t.Fatal("未找到可用的中文字体：水印会静默失效")
	}
	src, err := os.ReadFile("/tmp/probe.png")
	if err != nil {
		t.Skip(err)
	}
	out := Watermark(src, []string{"2026-07-29 12:00:00", "GPS 31.23,121.47", "装货 · 张三 · YD001"})
	if len(out) < 3 || out[0] != 0xff || out[1] != 0xd8 {
		t.Fatalf("输出不是 JPEG：%x", out[:3])
	}
	if len(out) == len(src) {
		t.Fatal("水印未生效（原样返回）")
	}
	_ = os.WriteFile("/tmp/wm_go.jpg", out, 0o644)
	t.Logf("in=%dB out=%dB", len(src), len(out))
}
