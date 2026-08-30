package httpx_test

// 预检放行的请求头必须覆盖前端真的会发的那些。
//
// 由来：司机端发 X-Driver-Token，而 Access-Control-Allow-Headers 里只有
// Authorization 和 Content-Type。表现是：登录（不带自定义头）200，
// 之后 /driver/tasks 的预检被浏览器挡下，界面一片空白只写 "Failed to fetch"，
// **服务端日志上一行异常都没有**——请求根本没发出来。
// 五个司机端接口全废，而后端用例、tsc、接口对齐走查全绿。
//
// 所以这条用例不写死一个名单去比对（那样下次加头照样会漏），
// 而是**去扫前端源码**：凡是 headers.set("X-…") 出现过的头，
// 都必须在 AllowedRequestHeaders 里。加一个漏一个就红。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var setHeaderRe = regexp.MustCompile(`headers\.set\(\s*["']([A-Za-z0-9-]+)["']`)

func TestCORSAllowsEveryCustomHeaderTheFrontendSends(t *testing.T) {
	root := ""
	for _, p := range []string{"../../../frontend/src", "../../frontend/src"} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			root = p
			break
		}
	}
	if root == "" {
		// 找不到前端源码就等于什么都没验。这类"悄悄不跑"正是这一轮反复吃亏的地方，
		// 所以判失败而不是跳过。
		t.Fatal("找不到 frontend/src，这条用例会空转——请修路径而不是让它静默通过")
	}

	allowed := map[string]bool{}
	for _, h := range httpx.AllowedRequestHeaders {
		allowed[strings.ToLower(h)] = true
	}

	var sent []string
	seen := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if ext := filepath.Ext(path); ext != ".ts" && ext != ".tsx" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range setHeaderRe.FindAllStringSubmatch(string(src), -1) {
			name := strings.ToLower(m[1])
			// 简单请求头（CORS 默认放行）不需要声明
			if name == "content-type" || name == "accept" || name == "accept-language" {
				continue
			}
			if !seen[name] {
				seen[name] = true
				sent = append(sent, m[1]+"（"+path+"）")
			}
			if !allowed[name] {
				t.Errorf("前端会发请求头 %q（%s），但它不在 httpx.AllowedRequestHeaders 里：\n"+
					"  浏览器会在预检阶段挡下这个请求，服务端什么都看不到，界面只报 Failed to fetch。\n"+
					"  把它加到 middleware.go 的 AllowedRequestHeaders。", m[1], path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫前端源码失败：%v", err)
	}
	// 防空转：正则失效时一个头都扫不到，比较结果恒为"全都放行了"。
	// 前端至少会发 Authorization（api/client.ts）和 X-Driver-Token（司机端）。
	if len(sent) == 0 {
		t.Fatal("一个自定义请求头都没扫到——正则失效了，这条用例正在空转")
	}
	t.Logf("前端发出的自定义请求头 %d 个：%s", len(sent), strings.Join(sent, "、"))
}
