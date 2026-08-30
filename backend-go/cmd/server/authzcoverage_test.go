package main

// 每一个会改数据的动作路由，都必须挂着权限闸。
//
// 由来：发布前拿一个只有 masterdata.view + waybill.view 的客服账号，
// 把 100 条动作路由挨个打了一遍——**53 条没有被 403 挡住**。其中包括：
//
//   POST /finance/pricing-rules          建一条 priority=9999 的通配收入价（实测 201）
//   POST /finance/reimbursements/{id}/pay 把报销标记成已付
//   POST /waybills/{no}/sign             替别人签收
//   POST /waybills/{no}/transition       任意推进运单状态
//   POST /waybills/{no}/remit-cod        代收货款打款
//   POST /exceptions/{id}/close          定责并落一条应付
//
// 根因有两层：
//   1. 通用 CRUD 引擎的 gate 语义是「want 为空 = 不设限」，
//      而 22 个读写配置里有 22 处没写全 ReadPerm/WritePerm。
//   2. 自己取参数、不走 resolve(w, r, perm) 的 handler 全都漏了权限检查，
//      挡住其中一部分的只是**数据范围**——而数据范围管的是"看得见谁的单"，
//      不是"能不能做这件事"：同一个网点的客服照样能替公司定责赔钱。
//
// authz_test.go 那份清单是**逐条登记**的，登记了就守得住，没登记的就守不住——
// 而"忘了登记"和"忘了挂闸"往往是同一次疏忽。这条用例换一个角度：
// 不看清单，直接扫源码，要求每个写动作 handler 里都出现过一次权限检查。
//
// 它抓不到什么：只看"有没有检查"，不看"检查的是不是对的权限点"。
// 那一层由 authz_test.go 的期望码清单守。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 允许没有权限检查的 handler，每条都要写清为什么。
var authzExempt = map[string]string{
	"PublicIntake": "对外开放的客户自助下单，本就免登录（有独立限流）",
	"Quote":        "报价预览，不落库（路由层按 waybill.view 放行）",
	"ParsePreview": "解析预览，不落库",
	"Read":         "标记自己的通知已读，没有别人的数据可动",
	"ReadAll":      "同上",
	"PublicTrack":  "免登录查单，有两道限流",
	// 司机端：走的是司机令牌（authDriver），不是后台用户的权限点体系。
	// 它们各自校验"这张运单是不是本人的"，那才是这一侧该有的边界。
	"Login":            "司机端登录",
	"Checkin":          "司机打卡，authDriver + 本人运单校验",
	"UploadCredential": "司机自助传证件，同上",
	"AckReminder":      "司机确认提醒，同上",
	// 设备侧：走设备/网关凭据，不走用户权限
	"Ingest":         "车载终端上报",
	"TrackingIngest": "轨迹点上报",
}

func TestEveryMutatingHandlerHasAPermissionCheck(t *testing.T) {
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("读 main.go 失败：%v", err)
	}
	// main.go 里被注册为写方法的 handler 名
	reg := regexp.MustCompile(`\.(?:Post|Patch|Delete|Put)\(\s*"[^"]+",\s*\w+\.(\w+)`)
	wanted := map[string]bool{}
	for _, m := range reg.FindAllStringSubmatch(string(mainSrc), -1) {
		wanted[m[1]] = true
	}
	if len(wanted) < 20 {
		t.Fatalf("只从 main.go 里扫到 %d 个写 handler——正则失效了，这条用例正在空转", len(wanted))
	}

	// 各包的权限闸叫法不一样，但都是"取当前用户 → 比权限点 → 不过就 403"。
	// require 是 AI 那个包的写法（比 ai.use）。
	// allowAny 是"任一权限点满足即放行"，建单那几条用它
	// （客服有 waybill.create，调度员只勾了 waybill.manage）。
	// 它是真闸，只是这条用例原先不认识它——不登记的话建单会被误报成"没挂闸"。
	guard := regexp.MustCompile(`Allow\(w, r,|requirePerm\(w, r,|resolve\(w, r,|Guard\(w, r,|` +
		`allow\(w, r,|allowAny\(w, r,|need\(w, r,|require\(w, r\)|guard\(w, r\)|MD\.Allow\(|` +
		`approvalGate\(w, r|codAction\(w, r`)
	fn := regexp.MustCompile(`func \(h \*Handler\) (\w+)\(w http\.ResponseWriter, r \*http\.Request\) \{`)
	factory := regexp.MustCompile(`func \(h \*Handler\) (\w+)\([^)]*\) http\.HandlerFunc \{`)

	checked, missing := 0, []string{}
	err = filepath.Walk("../../internal", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		for _, pat := range []*regexp.Regexp{fn, factory} {
			for _, m := range pat.FindAllStringSubmatchIndex(src, -1) {
				name := src[m[2]:m[3]]
				if !wanted[name] {
					continue
				}
				if _, ok := authzExempt[name]; ok {
					continue
				}
				checked++
				// 函数体：从 { 到下一个顶格 } 之间
				body := src[m[1]:]
				if end := strings.Index(body, "\n}\n"); end > 0 {
					body = body[:end]
				}
				if !guard.MatchString(body) {
					missing = append(missing, name+"（"+filepath.Base(path)+"）")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫源码失败：%v", err)
	}
	if checked < 20 {
		t.Fatalf("只对上了 %d 个 handler——命名约定变了，这条用例正在空转", checked)
	}
	if len(missing) > 0 {
		t.Errorf("这些 handler 会改数据但函数体里没有任何权限检查（%d 个）：\n  %s\n\n"+
			"挡住它们的可能只是数据范围，而数据范围管的是「看得见谁的单」，"+
			"不是「能不能做这件事」。\n"+
			"要么加一句 h.allow(w, r, \"<权限点>\")，"+
			"要么在 authzExempt 里写明为什么不需要。",
			len(missing), strings.Join(missing, "\n  "))
	}
	t.Logf("扫过 %d 个写动作 handler，全部带权限检查（另有 %d 个已声明豁免）", checked, len(authzExempt))
}
