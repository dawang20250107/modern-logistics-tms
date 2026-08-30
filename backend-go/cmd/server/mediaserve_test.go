package main

// 上传物不能在应用自己的域名下被当成网页执行。
//
// 由来：三条上传路径都用 `http.DetectContentType` 按**内容**嗅探类型。
// 传一个 .html 上去，存下来的类型就是 text/html，
// 再从 /media/ 原样吐出来——脚本就在应用的同源里跑起来了。
// 实测传 `<script>alert(document.domain)</script>`：
//
//	GET /media/attachments/<uuid>.html
//	200  Content-Type: text/html; charset=utf-8   （内容原样）
//
// `X-Content-Type-Options: nosniff` 挡不住这个：它只禁止浏览器**猜**类型，
// 而这里声明出来的类型本身就是 text/html。
//
// 最短的攻击路径是司机端：/api/v1/driver/credentials 是**自助上传**，
// 只要司机令牌（手机号 + 身份证后 6 位）就能传。司机种一个 HTML 当"证件"，
// 客服在后台点开那条链接，脚本带着客服的会话在同源里执行。
//
// 修法是在**发出去那一侧**判：能安全内联的按原类型发（图片和 PDF 必须
// 能直接看——回单照片、证件都是这么看的），其余降成八位字节流并强制下载。
// 放在发出侧而不是上传侧，是因为它同时盖住了**已经存进去的文件**，
// 不用去翻历史数据；而且以后新增上传口也自动被盖住。

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// putMedia 直接往存储里放一个文件，返回 /media/ 下的 key。
// 不走上传接口：这条用例要验的是**发出去那一侧**，
// 上传口有几条、各自怎么判类型，是另一件事。
func (e *testEnv) serveMedia(t *testing.T, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/media/"+key, nil)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) uploadAttachmentNamed(t *testing.T, token, orderID, filename string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("kind", "other")
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("造 multipart 失败：%v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/orders/"+orderID+"/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		t.Fatalf("上传 %s 失败 %d：%s", filename, rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	i := strings.Index(body, `"file":"`)
	if i < 0 {
		t.Fatalf("返回体里没有 file 字段：%s", body)
	}
	rest := body[i+len(`"file":"`):]
	return rest[:strings.Index(rest, `"`)]
}

func TestUploadedHTMLIsNotServedAsAPage(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	orderID := e.mkOrder()

	key := e.uploadAttachmentNamed(t, token, orderID,
		"正常报关单.html",
		[]byte(`<html><body><script>alert(document.domain)</script></body></html>`))
	// 文件本身留在 media 目录里：这条用例验的是响应头，
	// 而备份演练每次会连库带媒体一起重来，不必单独清。

	rec := e.serveMedia(t, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("取回 %s 返回 %d —— 用例前提不成立", key, rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if strings.Contains(strings.ToLower(ct), "text/html") {
		t.Errorf("上传的 HTML 被以 %q 发了出去 —— 浏览器会把它当页面渲染，"+
			"脚本在应用的同源里执行。\n"+
			"  nosniff 挡不住这个：它只禁止浏览器「猜」类型，而这里声明的就是 text/html。", ct)
	}
	if d := rec.Header().Get("Content-Disposition"); !strings.Contains(d, "attachment") {
		t.Errorf("不能内联的类型没有带 Content-Disposition: attachment（实际 %q）", d)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff 头没了 —— 那是另一半保障")
	}
}

func TestImagesStillServeInline(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	orderID := e.mkOrder()

	// 最小的合法 PNG（1×1）。回单照片、司机证件都要能在界面上直接显示，
	// 把它们也降成下载就是修坏了产品。
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
		0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1,
		0x0d, 0x0a, 0x2d, 0xb4,
		0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}
	key := e.uploadAttachmentNamed(t, token, orderID, "回单.png", png)
	rec := e.serveMedia(t, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("取回 %s 返回 %d", key, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("图片被发成了 %q —— 回单照片和证件必须能在界面上直接显示", ct)
	}
	if d := rec.Header().Get("Content-Disposition"); strings.Contains(d, "attachment") {
		t.Errorf("图片被强制下载了（%q）—— 那样界面上就看不到回单了", d)
	}
}
