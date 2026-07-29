package driver

// 打卡照片水印：在左下角叠加半透明黑底 + 时间 / 定位 / 节点·司机·运单号 三行白字。
//
// 这不是装饰：打卡照片是运输过程的现场证据，水印把「拍摄时间 + GPS + 业务上下文」
// 焊死在像素里，事后无法靠改数据库洗掉，因此必须跟着一起移植。
//
// 版式与 apps/ops/watermark.py 一致（字号 = 宽/40 且不小于 14，padding = 字号/2，
// 行高 = 字号 + padding/2，底栏透明度 110/255，正文 235/255）。字体优先系统 CJK，
// 找不到则与 Python 侧的 load_default 回退同义——不阻断打卡，只是没有中文字形。

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // 打卡照片可能是 PNG
	"os"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// 候选中文字体：按优先级探测，取第一个「能被 sfnt 解析且确实有中文字形」的。
// 只看路径存在是不够的——wqy-zenhei.ttc 在本机就能被 fc-list 列出、Pillow 也能用，
// 但 x/image/font/sfnt 解析它会报 invalid table offset，落到静默不水印。
// 覆盖字形是水印的全部意义，所以探测必须验到字形这一层。
var cjkFontPaths = []string{
	"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansSC-Regular.otf",
	"/usr/share/fonts/opentype/unifont/unifont.otf",
	"/usr/share/fonts/opentype/unifont/unifont_jp.otf",
	"/System/Library/Fonts/PingFang.ttc",
}

// FontPathEnv 允许部署时显式指定字体，优先于内置候选
const FontPathEnv = "WATERMARK_FONT"

// hasCJK 校验字体确实能渲染常用汉字（打卡水印里必然出现「装/货/车」这类字）
func hasCJK(f *sfnt.Font) bool {
	var buf sfnt.Buffer
	for _, r := range []rune{'装', '货', '车'} {
		g, err := f.GlyphIndex(&buf, r)
		if err != nil || g == 0 {
			return false
		}
	}
	return true
}

// parseFontFile 单体字体与字体集合都试一遍，返回首个可用且含中文字形的
func parseFontFile(raw []byte) *sfnt.Font {
	if f, err := sfnt.Parse(raw); err == nil && hasCJK(f) {
		return f
	}
	col, err := sfnt.ParseCollection(raw)
	if err != nil {
		return nil
	}
	for i := 0; i < col.NumFonts(); i++ {
		if f, err := col.Font(i); err == nil && hasCJK(f) {
			return f
		}
	}
	return nil
}

var (
	fontOnce sync.Once
	cjkFont  *sfnt.Font
)

func loadCJKFont() *sfnt.Font {
	fontOnce.Do(func() {
		paths := cjkFontPaths
		if p := os.Getenv(FontPathEnv); p != "" {
			paths = append([]string{p}, paths...)
		}
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if f := parseFontFile(raw); f != nil {
				cjkFont = f
				return
			}
		}
	})
	return cjkFont
}

// Watermark 叠加水印并输出 JPEG；任何失败都原样返回入参，绝不阻断打卡。
func Watermark(raw []byte, lines []string) []byte {
	f := loadCJKFont()
	if f == nil {
		return raw
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw // 非图片（如 PDF）原样返回
	}
	b := src.Bounds()
	img := image.NewRGBA(b)
	draw.Draw(img, b, src, b.Min, draw.Src)

	size := b.Dx() / 40
	if size < 14 {
		size = 14
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size: float64(size), DPI: 72, Hinting: font.HintingFull,
	})
	if err != nil {
		return raw
	}
	defer face.Close()

	pad := size / 2
	lineH := size + pad/2
	boxH := lineH*len(lines) + pad
	y0 := b.Max.Y - boxH

	// 半透明黑底（Pillow 的 RGBA rectangle 走的是 over 合成，此处同义）
	bar := image.Rect(b.Min.X, y0, b.Max.X, b.Max.Y)
	draw.DrawMask(img, bar, image.NewUniform(color.Black), image.Point{},
		image.NewUniform(color.Alpha{A: 110}), image.Point{}, draw.Over)

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 235}),
		Face: face,
	}
	y := y0 + pad/2
	for _, ln := range lines {
		// Pillow 的 text() 以文字框左上角定位，Go 的 Drawer 以基线定位：补一个 ascent
		d.Dot = fixed.P(b.Min.X+pad, y+face.Metrics().Ascent.Round())
		d.DrawString(ln)
		y += lineH
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return raw
	}
	return out.Bytes()
}
