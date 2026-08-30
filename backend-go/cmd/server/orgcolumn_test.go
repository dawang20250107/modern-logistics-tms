package main

// 带组织归属的表，INSERT 时不能漏掉那一列。
//
// 由来：拿演示客服（数据范围 org=上海）真的登进去点，它在订单管理里
// 看得见自己建的单，点开对应运单却是「运单不存在、无权访问」。
// 根因是 `ops_waybill.organization_id` 决定运单的数据范围，
// 而主派单路径和批量派承运商**压根没写这一列**——落库是 NULL，
// 而 NULL 只有「全部」档看得见。
//
// 漏这一列不会报错、不会崩、类型检查也过。它的表现是"某些人看不见某些数据"，
// 而看不见的人多半会以为是自己权限不够，不会来报 bug。
//
// 所以查源码：凡是往带 organization_id 的表里 INSERT，
// 要么写上这一列，要么在 orgColumnExempt 里说明为什么不需要。
//
// 这条检查自己踩过一次坑：列名常常是 `... , `+waybillCopyCols+`, ...` 这样
// 拼出来的，只看字面量会把好的判成漏的（运单拆单那两处就是）。
// 所以先把同包里的字符串常量展开再判——**误报的检查比没有检查更坏**。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 带 organization_id 但 INSERT 时确实不必写的地方，每条写清理由。
var orgColumnExempt = map[string]string{
	"cmd/adminctl/main.go|accounts_user":   "引导用的超级管理员：它要跨组织看全部，本来就不该挂在任何一个组织下",
	"cmd/seed/main.go|iam_role_assignment": "演示数据的角色分配不按组织细分，范围由角色上的 data_scope 决定",
}

func TestInsertsKeepOrganizationColumn(t *testing.T) {
	// 这几张表上的 organization_id 是数据范围的依据
	scoped := map[string]bool{
		"ops_waybill": true, "ops_dispatch_batch": true, "fin_statement": true,
		"accounts_user": true, "iam_employee": true, "iam_department": true,
		"iam_service_area": true, "iam_api_key": true, "iam_role_assignment": true,
	}
	constPat := regexp.MustCompile("(?s)const (\\w+) = `([^`]*)`")
	insPat := regexp.MustCompile(`(?s)INSERT INTO (\w+)\s*\(([^)]*)\)`)

	scanned, missing := 0, []string{}
	root := "../.."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		// 同一个文件里的字符串常量：列名经常是拼出来的
		consts := map[string]string{}
		for _, m := range constPat.FindAllStringSubmatch(src, -1) {
			consts[m[1]] = m[2]
		}
		rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(path, root)), "/")
		for _, m := range insPat.FindAllStringSubmatchIndex(src, -1) {
			table := src[m[2]:m[3]]
			if !scoped[table] {
				continue
			}
			scanned++
			cols := src[m[4]:m[5]]
			// 展开 `+ident+`
			for name, val := range consts {
				cols = strings.ReplaceAll(cols, "`+"+name+"+`", val)
			}
			// 去掉 SQL 注释，免得注释里提到列名就算数
			cols = regexp.MustCompile(`--[^\n]*`).ReplaceAllString(cols, "")
			if strings.Contains(cols, "organization_id") {
				continue
			}
			if _, ok := orgColumnExempt[rel+"|"+table]; ok {
				continue
			}
			missing = append(missing, rel+" → "+table)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫源码失败：%v", err)
	}

	// 空转防护：正则或目录结构变了会一条都扫不到，
	// 那时候"全都合规"和"根本没检查"长得一模一样。
	if scanned < 8 {
		t.Fatalf("只扫到 %d 条对带组织归属的表的 INSERT —— 多半失配了，这次结论不作数", scanned)
	}
	t.Logf("扫了 %d 条 INSERT", scanned)

	if len(missing) > 0 {
		t.Errorf("这些 INSERT 漏了 organization_id（共 %d 处）：\n  %s\n\n"+
			"这一列决定数据范围，落成 NULL 就只有「全部」档看得见——"+
			"表现是某些人看不见某些数据，而看不见的人多半以为是自己权限不够，不会来报。\n"+
			"要么写上这一列，要么在 orgColumnExempt 里说明为什么不需要。",
			len(missing), strings.Join(missing, "\n  "))
	}
}
