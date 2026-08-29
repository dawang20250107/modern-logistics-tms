package main

// /media 路由的守卫。
//
// 这条路由原先是 http.StripPrefix("/media/", http.FileServer(http.Dir(root)))。
// 接对象存储之后必须自己从 blob.Store 读——而 FileServer **顺手提供**的两样
// 保护也就跟着没了：
//   · 路径穿越清理
//   · 不列目录（准确说 FileServer 会列，是外面那层尾斜杠判断在挡）
//
// 这类"本来由框架白送"的保障是换实现时最容易悄悄丢的：不会编译报错，
// 正常用例也照过，只有专门去打才看得出来。所以单独钉一组。

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaServesAndGuards(t *testing.T) {
	e := newTestEnv(t)

	// 在媒体根下放一个真文件，同时在根**外面**放一个"机密文件"
	root := os.Getenv("MEDIA_ROOT")
	if root == "" {
		root = "./media"
	}
	dir := filepath.Join(root, "mediatest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("媒体目录不可写，跳过：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("MEDIA-OK"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "..", "media-test-secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Skipf("无法写测试文件，跳过：%v", err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })

	t.Run("正常取件", func(t *testing.T) {
		rec := e.call("", "GET", "/media/mediatest/ok.txt", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("期望 200，实得 %d", rec.Code)
		}
		if body := rec.Body.String(); body != "MEDIA-OK" {
			t.Errorf("内容 %q，期望 MEDIA-OK", body)
		}
		// 用户上传物必须带 nosniff，否则浏览器会按内容猜类型——
		// 一个伪装成 .txt 的 HTML 就能变成存储型 XSS
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options=%q，必须是 nosniff", got)
		}
	})

	t.Run("路径穿越", func(t *testing.T) {
		for _, p := range []string{
			"/media/../media-test-secret.txt",
			"/media/../../etc/passwd",
			"/media/mediatest/../../media-test-secret.txt",
			"/media/./mediatest/ok.txt",
			"/media/mediatest/..",
		} {
			rec := e.call("", "GET", p, "")
			if rec.Code == http.StatusOK {
				t.Errorf("%s 竟然回了 200，内容：%q", p, truncate(rec.Body.String(), 80))
			}
			if strings.Contains(rec.Body.String(), "SECRET") {
				t.Errorf("%s 读到了根目录外的文件", p)
			}
		}
	})

	t.Run("不列目录", func(t *testing.T) {
		for _, p := range []string{"/media/", "/media/mediatest", "/media/mediatest/"} {
			rec := e.call("", "GET", p, "")
			if rec.Code == http.StatusOK {
				t.Errorf("%s 回了 200（可能列出了目录）：%q", p, truncate(rec.Body.String(), 120))
			}
			if strings.Contains(rec.Body.String(), "ok.txt") {
				t.Errorf("%s 把目录内容列出来了", p)
			}
		}
	})

	t.Run("不存在的对象回 404 且不带底层细节", func(t *testing.T) {
		rec := e.call("", "GET", "/media/mediatest/nope.png", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("期望 404，实得 %d", rec.Code)
		}
		// 底层错误（路径、bucket 名）不能出现在响应里
		for _, leak := range []string{"/home/", "no such file", "bucket"} {
			if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
				t.Errorf("404 响应里泄漏了 %q：%s", leak, rec.Body.String())
			}
		}
	})
}
