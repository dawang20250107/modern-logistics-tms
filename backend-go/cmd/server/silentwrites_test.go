package main

// 写库的错误不许被丢掉。
//
// `_, _ = h.DB.Exec(...)` 这个写法的代价这一轮见过三次：
//
//   · 回单状态重算：Postgres 推断不出参数类型直接报错，错误被吞，
//     接口 200，状态没变。查了很久才发现"按了没反应"是这么来的。
//   · 批量确认/进池/取消：四个 UPDATE 全是这个写法，然后一律 return 成功。
//     50 单批量操作哪怕每条都失败，界面照样弹「已处理 50 单」，库里一条没变。
//   · 对账单审计的时间戳：失败时 auditedAt 是零值，响应带着
//     "audited_at":"0001-01-01T00:00:00Z" 回去，界面显示"已审计"——
//     而对账是要拿去跟客户对话的。
//
// 共同点：**这类失败没有任何人会知道**。没有报错、没有日志、没有异常状态码，
// 只有"我明明点了"和"库里没有"之间那道对不上的缝。
//
// 所以现在一律要么判错返回、要么至少 slog 一句。这条用例扫的是源码里
// 还有没有 `_, _ = ...Exec(`。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNoSilentlyDiscardedWrites(t *testing.T) {
	// 只匹配真正的语句（行首缩进后紧跟），不匹配注释里提到这个写法的文字
	pat := regexp.MustCompile(`(?m)^\s*_, _ = \w+(?:\.\w+)*\.Exec\(`)
	var hits []string
	scanned := 0
	err := filepath.Walk("../../internal", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		src := string(raw)
		for _, m := range pat.FindAllStringIndex(src, -1) {
			line := strings.Count(src[:m[0]], "\n") + 1
			hits = append(hits, strings.TrimPrefix(path, "../../")+":"+itoa(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫源码失败：%v", err)
	}
	// 防空转：一个文件都没扫到说明路径变了
	if scanned < 20 {
		t.Fatalf("只扫了 %d 个 .go 文件——路径不对，这条用例正在空转", scanned)
	}
	if len(hits) > 0 {
		t.Errorf("这些地方把写库的错误丢掉了（%d 处）：\n  %s\n\n"+
			"这类失败没有任何人会知道：没有报错、没有日志、没有异常状态码，\n"+
			"只有「我明明点了」和「库里没有」之间那道对不上的缝。\n"+
			"要么判错返回，要么至少 slog.Warn 一句。",
			len(hits), strings.Join(hits, "\n  "))
	}
	t.Logf("扫过 %d 个 .go 文件，没有被丢掉的写库错误", scanned)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
