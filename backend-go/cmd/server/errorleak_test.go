package main

// 5xx 的响应体里不能带数据库原话。
//
// 由来：62 处写成 `httpx.Err(w, 500, "INTERNAL", "写入失败："+err.Error())`。
// 那句 err.Error() 是 Postgres 的原话：
//
//	更新失败：ERROR: invalid input syntax for type numeric: "一万" (SQLSTATE 22P02)
//
// 两个问题。一是**对用户没用**：他看不懂 SQLSTATE，也不知道该改哪一栏。
// 二是**把库的结构说出去了**：列名、列类型、约束名，有时还带上值。
// 公开下单那条路早就单独修过（不给匿名调用方看内部错误），
// 但登录之后那 60 多处一直照抛——而"登录了"不等于"该看见数据库长什么样"，
// 客服和司机也是登录用户。
//
// 现在统一走 httpx.Fail：原文进日志（带方法和路径，查得到），
// 回给调用方的是一句人话。这条用例守住不再往回退。
//
// 4xx 不在此列：那是"你填错了"，带上是哪一栏、错在哪里正是有用的信息。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNo5xxLeaksDatabaseErrors(t *testing.T) {
	// httpx.Err(w, http.StatusInternalServerError, …, …+err.Error())
	leak := regexp.MustCompile(
		`httpx\.Err\(\s*w,\s*http\.StatusInternalServerError,[^)]*\.Error\(\)`)
	// 顺带挡住 502：外部集成的报错同样不该原样回给前端
	leak502 := regexp.MustCompile(
		`httpx\.Err\(\s*w,\s*http\.StatusBadGateway,[^)]*\.Error\(\)`)

	scanned, bad := 0, []string{}
	err := filepath.Walk("../..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		scanned++
		for _, m := range leak.FindAllString(src, -1) {
			bad = append(bad, path+"：500 带上了 err.Error()  "+strings.TrimSpace(m))
		}
		for _, m := range leak502.FindAllString(src, -1) {
			bad = append(bad, path+"：502 带上了 err.Error()  "+strings.TrimSpace(m))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫源码失败：%v", err)
	}
	// 空转防护：目录结构变了会一个文件都扫不到
	if scanned < 50 {
		t.Fatalf("只扫到 %d 个 .go 文件 —— 目录多半变了，这次结论不作数", scanned)
	}
	// 反向自检：httpx.Fail 必须真的存在且被用上，否则这条用例只是"没匹配到"
	all, _ := os.ReadFile("../../internal/httpx/envelope.go")
	if !strings.Contains(string(all), "func Fail(") {
		t.Fatal("httpx.Fail 不见了 —— 那这条检查守的东西已经没有了")
	}

	if len(bad) > 0 {
		t.Errorf("这些地方把内部错误原文回给了调用方（%d 处）：\n  %s\n\n"+
			"改用 httpx.Fail(w, r, code, 一句人话, err)：原文进日志，前端拿到的是能照着做的话。\n"+
			"（4xx 不受这条限制——那是「你填错了」，带上细节正是有用的。）",
			len(bad), strings.Join(bad, "\n  "))
	}
}
