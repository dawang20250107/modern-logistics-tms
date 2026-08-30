package main

// 库里的枚举取值，必须都在词表里。
//
// 由来：我自己的走查脚本用 `fulltime` 造司机——这个值在三份词表里
// **一处都不存在**（后端 employmentLabel、列表 SQL 的 CASE、前端
// DRIVER_EMP_LABEL 都是 employee/outsourced/carrier_driver/temp）。
// 它一路安静地写进去，攒到 763 个在用司机里有 758 个是它。
//
// 表现不是报错：列表 SQL 对未知值回空串，界面显示「—」。
// 所以「用工」这一列对九成以上的司机是空的，而且**按用工筛选永远筛不到它们**——
// 按「自有员工」筛只出 5 个（那 5 个是种子造的）。
// 拿这样的库去做验收，看到的是一个"数据不全"的产品。
//
// 根因是 employment_type 声明成了自由文本。已经改成枚举，
// 第一次写非法值就会被 400 挡住。这条用例守另一半：
// **库里已经有的数据也得对**——种子、导入、历史数据都可能带进坏值，
// 而坏值不报错，只是让某一列悄悄空掉。
//
// 名单来源写在每一条旁边。加字段时顺手加一行，比事后找为什么某列是空的便宜得多。

import (
	"context"
	"strings"
	"testing"
)

func TestDatabaseValuesAreInVocabulary(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	for _, c := range []struct {
		table, col string
		allowed    []string
		source     string
	}{
		{"md_driver", "employment_type",
			[]string{"employee", "outsourced", "carrier_driver", "temp"},
			"waybills/handler.go 的 employmentLabel、masterdata 列表 SQL 的 CASE、前端 DRIVER_EMP_LABEL"},
		{"md_customer", "level",
			[]string{"S", "A", "B", "C", "D"},
			"前端 CUSTOMER_LEVEL_LABEL"},
		{"md_customer", "category",
			[]string{"individual", "enterprise", "government"},
			"前端 FleetPage 的 CUST_CATEGORY_LABEL"},
		{"md_customer", "settlement_type",
			[]string{"monthly", "cash", "prepaid"},
			"前端 SETTLEMENT_LABEL"},
		{"ops_order", "priority",
			[]string{"normal", "urgent", "vip"},
			"orders/transitions.go 的 priorityLabel"},
		{"ops_order", "business_type",
			[]string{"ftl", "ltl", "express", "coldchain", "hazmat"},
			"orders/transitions.go 的 businessTypeLabel"},
		{"ops_order", "freight_term",
			[]string{"prepaid", "collect", "receipt", "monthly"},
			"orders/handler.go 的 freightTermLabel"},
		{"ops_order", "freight_payer",
			[]string{"shipper", "consignee", "third_party"},
			"orders/handler.go 的 freightPayerLabel"},
		{"ops_waybill", "cod_status",
			[]string{"none", "pending", "collected", "remitted"},
			"waybills/handler.go 的 codStatusLabel"},
	} {
		rows, err := e.pool.Query(ctx,
			"SELECT DISTINCT "+c.col+", count(*) OVER (PARTITION BY "+c.col+") FROM "+c.table+
				" WHERE COALESCE("+c.col+",'') <> ''")
		if err != nil {
			t.Errorf("查 %s.%s 失败：%v", c.table, c.col, err)
			continue
		}
		type badVal struct {
			v string
			n int
		}
		var bad []badVal
		seen := 0
		for rows.Next() {
			var v string
			var n int
			if err := rows.Scan(&v, &n); err != nil {
				t.Fatalf("扫描失败：%v", err)
			}
			seen++
			ok := false
			for _, a := range c.allowed {
				if v == a {
					ok = true
					break
				}
			}
			if !ok {
				bad = append(bad, badVal{v, n})
			}
		}
		rows.Close()
		// 空转防护：一个值都没读到说明这张表是空的，这一条什么也没测到
		if seen == 0 {
			t.Logf("%s.%s 没有非空取值，跳过", c.table, c.col)
			continue
		}
		for _, b := range bad {
			t.Errorf("%s.%s 里有 %d 行的取值是 %q，而词表里没有它。\n"+
				"  词表：%s（来源：%s）\n"+
				"  这种值不会报错——界面上那一列会空着，按它筛选也永远筛不到这些行。",
				c.table, c.col, b.n, b.v, strings.Join(c.allowed, " / "), c.source)
		}
	}
}
