package driver

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// probePNG 造一张纯色图当底片。原来这里读 /tmp/probe.png、读不到就 Skip，
// 结果是 CI 上永远只跑到「有没有字体」那一句，真正的渲染路径从没被验过。
func probePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 210, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("造底片失败：%v", err)
	}
	return buf.Bytes()
}

// 水印是打卡照片的证据属性所在：没字体就静默不水印，等于悄悄丢掉证据，
// 因此这条测试守的是「本机确实能渲染中文水印」。
func TestWatermarkRenders(t *testing.T) {
	if loadCJKFont() == nil {
		t.Fatal("未找到可用的中文字体：水印会静默失效。装一个 CJK 字体，" +
			"或用 " + FontPathEnv + " 指定")
	}
	t.Logf("使用字体：%s", FontPath())

	src := probePNG(t, 1200, 800)
	out := Watermark(src, []string{"2026-07-29 12:00:00", "GPS 31.23,121.47", "装货 · 张三 · YD001"})
	if len(out) < 3 || out[0] != 0xff || out[1] != 0xd8 {
		t.Fatalf("输出不是 JPEG：%x", out[:min(3, len(out))])
	}
	if bytes.Equal(out, src) {
		t.Fatal("水印未生效（原样返回）")
	}

	// 底栏必须真的被画上去：解码回来比像素。只看「字节变了」是不够的，
	// 重编码一次 JPEG 也会让字节变——那正是"静默不水印"会伪装成的样子。
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("回读输出失败：%v", err)
	}
	b := img.Bounds()
	br, bg, bb, _ := img.At(b.Min.X+5, b.Max.Y-5).RGBA() // 左下角，落在半透明黑底里
	tr, tg, tb, _ := img.At(b.Min.X+5, b.Min.Y+5).RGBA() // 左上角，应保持原色
	if br >= tr || bg >= tg || bb >= tb {
		t.Fatalf("左下角未被压暗，底栏没画上：底=(%d,%d,%d) 顶=(%d,%d,%d)", br, bg, bb, tr, tg, tb)
	}
}

// 非图片输入必须原样返回，绝不阻断打卡上传
func TestWatermarkPassesThroughNonImage(t *testing.T) {
	raw := []byte("%PDF-1.4 not an image")
	if out := Watermark(raw, []string{"x"}); !bytes.Equal(out, raw) {
		t.Fatal("非图片应原样返回")
	}
}
