package main

// 代码里校验的每一个权限点，都必须在目录里。
//
// 由来：`org.employee` **代码里一直在校验，目录里却没有**。它守着整个
// 员工管理面——员工档案增删改、启用停用、重置口令、账号交接、CSV 导入导出。
// 没有目录行就没有勾选框，任何角色都授不了它，于是客户的人事/管理员
// 要么当超管（连财务一起给出去），要么什么都干不了。
//
// 这和这个 PR 开头那处"权限点目录只有 3 行、代码却校验 12 个"是同一种缺口，
// 而**已有的那条用例没抓到它**——authz_test.go 的 TestPermissionCatalogCovers…
// 只比对 protectedEndpoints 这份**手工维护的清单**，而 org.employee 的那几个
// 端点从没被登记进去。"忘了登记"和"忘了挂闸"往往是同一次疏忽，
// 靠清单守不住没登记的那些。
//
// 这条不看清单，直接扫源码：凡是传给权限闸的字符串，都要在 auth.Catalog 里。

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
)

func TestEveryCheckedPermissionExistsInCatalog(t *testing.T) {
	inCatalog := map[string]bool{}
	for _, p := range auth.Catalog {
		inCatalog[p.Code] = true
	}

	// 两种写法：直接传给闸函数，以及写在读写配置的 ReadPerm/WritePerm 上
	// allowAny 一次传多个权限点，形如 allowAny(w, r, "a", "b")——
	// 单独列一条把后面那些也扫出来，否则新加的点会漏检。
	callPat := regexp.MustCompile(`(?:Allow|allowAny|allow|requirePerm|resolve|need|Guard|hasPerm)\(` +
		`(?:w, r, |ctx, |perms, )?"([a-z][a-z0-9_.]*)"(?:, "([a-z][a-z0-9_.]*)")*`)
	cfgPat := regexp.MustCompile(`(?:Read|Write)Perm:\s*"([a-z][a-z0-9_.]*)"`)

	where := map[string]map[string]bool{}
	err := filepath.Walk("../../internal", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		for _, pat := range []*regexp.Regexp{callPat, cfgPat} {
			for _, m := range pat.FindAllStringSubmatch(src, -1) {
				code := m[1]
				// 权限点一律带点（域.动作）。不带点的多半是别的字符串误匹配。
				if !strings.Contains(code, ".") {
					continue
				}
				if where[code] == nil {
					where[code] = map[string]bool{}
				}
				where[code][strings.TrimPrefix(path, "../../")] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫源码失败：%v", err)
	}
	// 防空转：扫不到权限点说明写法变了，而不是"全都在目录里"
	if len(where) < 8 {
		t.Fatalf("只扫到 %d 个被校验的权限点——正则失效了，这条用例正在空转", len(where))
	}

	var missing []string
	for code, files := range where {
		if inCatalog[code] {
			continue
		}
		var fs []string
		for f := range files {
			fs = append(fs, f)
		}
		sort.Strings(fs)
		missing = append(missing, code+"（"+strings.Join(fs, ", ")+"）")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("这些权限点代码里在校验，但不在 auth.Catalog 里（%d 个）：\n  %s\n\n"+
			"没有目录行就没有勾选框，任何角色都授不了它——"+
			"结果是这块功能只有超管能用，而超管意味着把别的域也一起给出去。",
			len(missing), strings.Join(missing, "\n  "))
	}
	t.Logf("扫到 %d 个被校验的权限点，全部在目录里（目录共 %d 个）", len(where), len(auth.Catalog))
}
