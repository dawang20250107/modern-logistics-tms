package main

// 对公开放的面不能把内部错误原文吐出去。
//
// 由来：客户在自助下单页的「重量(吨)」里填了「三吨」，得到的是
//
//   HTTP 500
//   {"code":"INTERNAL","message":"建单失败：ERROR: invalid input syntax for
//     type numeric: \"三吨\" (SQLSTATE 22P02)"}
//
// 两个问题叠在一起：
//   · 一个填错格子的客户应该拿到 400 和"哪一格填错了"，不是 500。
//   · 那句话把数据库引擎、列类型和 SQLSTATE 一起发给了**匿名调用方**。
// 而界面上显示的是「提交失败，请检查网络后重试」——指向一个错误的动作，
// 客户会反复重试，重试一万次结果一样。
//
// 这条用例扫的是"免登录能打到的 handler 里，有没有把 err.Error() 拼进响应"。
// 内部接口这么写是调试便利；公开面这么写是信息泄露。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 免登录能打到的 handler（对齐 main.go 里注册在 RequireAuth 之外的那些）。
// 加公开端点时要同步加到这里——漏加的代价是这条检查看不住它。
var publicHandlers = map[string][]string{
	"internal/orders/public.go":  {"PublicIntake", "PublicTrack"},
	"internal/driver/handler.go": {"Login", "Tasks", "Checkin", "UploadCredential", "AckReminder"},
}

func TestPublicEndpointsDoNotLeakInternalErrors(t *testing.T) {
	// 把 err.Error() 拼进给用户的消息里
	leak := regexp.MustCompile(`httpx\.Err\([^)]*\+\s*err\.Error\(\)`)
	checked := 0
	for rel, fns := range publicHandlers {
		raw, err := os.ReadFile(filepath.Join("../../", rel))
		if err != nil {
			t.Fatalf("读 %s 失败：%v（文件挪了就把 publicHandlers 一起改，别让这条静默失效）", rel, err)
		}
		src := string(raw)
		for _, fn := range fns {
			start := strings.Index(src, "func (h *Handler) "+fn+"(")
			if start < 0 {
				t.Errorf("%s 里找不到 %s——handler 改名了，这条检查正在空转", rel, fn)
				continue
			}
			checked++
			body := src[start:]
			if end := strings.Index(body[1:], "\nfunc "); end > 0 {
				body = body[:end]
			}
			for _, m := range leak.FindAllString(body, -1) {
				t.Errorf("%s 的 %s 把内部错误原文回给了匿名调用方：\n  %s\n"+
					"  原文只该进日志（slog.Error），回给对方的要是一句他能照着做的话。",
					rel, fn, strings.TrimSpace(m))
			}
		}
	}
	if checked < 5 {
		t.Fatalf("只对上了 %d 个公开 handler——名单或命名变了，这条用例正在空转", checked)
	}
	t.Logf("扫过 %d 个免登录 handler，没有把内部错误原文外发的", checked)
}
