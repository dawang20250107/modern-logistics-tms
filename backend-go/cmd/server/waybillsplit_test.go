package main

// 运单拆单/合单的货量守恒。
//
// 和订单拆单是同一类问题，但更容易出错：子单的件数/吨/方是**调用方给的**，
// 而父单随即作废。少给等于货凭空消失，多给等于凭空多出——
// 而吨数直接进承运商运费，多算是真金白银付出去。
//
// 这两条路径目前没有前端调用方（是给对接方/后台用的），但对所有已鉴权
// 调用方开放，而且一旦错了，错的是账。

import (
	"context"
	"fmt"
	"testing"
)

func (e *testEnv) waybillCargo(no string) (qty int, weight string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT cargo_quantity, cargo_weight_ton::text FROM ops_waybill WHERE waybill_no=$1`, no).
		Scan(&qty, &weight); err != nil {
		e.t.Fatalf("查运单货量失败：%v", err)
	}
	return qty, weight
}

// TestWaybillSplitMustConserveCargo 子单的货量之和必须等于父单。
//
// 少给：货凭空消失。多给：凭空多出，而吨数直接进承运商运费——
// 把 10 吨拆成两个 10 吨，等于凭空给承运商多算一倍的钱。
func TestWaybillSplitMustConserveCargo(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)

	for _, c := range []struct {
		name, body string
	}{
		{"少给（货凭空消失）", `{"splits":[{"cargo_quantity":1,"cargo_weight_ton":2},{"cargo_quantity":1,"cargo_weight_ton":2}]}`},
		{"多给（凭空多出，运费翻倍）", `{"splits":[{"cargo_quantity":10,"cargo_weight_ton":20},{"cargo_quantity":10,"cargo_weight_ton":20}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			// 每个用例一张新运单。共用一张的话，第一次拆完父单就 voided 了，
			// 第二次会因为"仅待调度前的运单可拆单"被拒——**用对的结果、错的理由**通过，
			// 这种绿比红更坏。
			no := e.mkWaybillAt("pending_dispatch")
			if _, err := e.pool.Exec(context.Background(),
				`UPDATE ops_waybill SET cargo_quantity=10, cargo_weight_ton=20 WHERE waybill_no=$1`, no); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = e.pool.Exec(context.Background(),
					`DELETE FROM ops_waybill WHERE waybill_no LIKE $1`, no+"-S%")
			})
			rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/split", c.body)
			if rec.Code < 400 {
				t.Errorf("拆出来的货量与父单对不上，接口却回 %d —— %s", rec.Code, c.name)
			}
		})
	}
}

// TestWaybillSplitConservedIsAccepted 对得上的拆法要放行，并且子单加起来等于父单。
func TestWaybillSplitConservedIsAccepted(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillAt("pending_dispatch")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE ops_waybill SET cargo_quantity=10, cargo_weight_ton=20, cargo_volume_cbm=30 WHERE waybill_no=$1`, no); err != nil {
		t.Fatal(err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			`DELETE FROM ops_waybill WHERE waybill_no LIKE $1`, no+"-S%")
	})

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/split",
		`{"splits":[{"cargo_quantity":4,"cargo_weight_ton":8,"cargo_volume_cbm":12},
		            {"cargo_quantity":6,"cargo_weight_ton":12,"cargo_volume_cbm":18}]}`)
	if rec.Code >= 400 {
		t.Fatalf("守恒的拆法被拒了 %d：%s", rec.Code, rec.Body.String())
	}

	var q int
	var wt, v string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT COALESCE(sum(cargo_quantity),0), COALESCE(sum(cargo_weight_ton),0)::text,
		       COALESCE(sum(cargo_volume_cbm),0)::text
		FROM ops_waybill WHERE waybill_no LIKE $1`, no+"-S%").Scan(&q, &wt, &v); err != nil {
		t.Fatalf("统计子单失败：%v", err)
	}
	if q != 10 || wt != "20.00" || v != "30.00" {
		t.Errorf("子单合计 %d 件 / %s 吨 / %s 方，父单是 10 / 20.00 / 30.00", q, wt, v)
	}
	_ = fmt.Sprint()
	if st, _ := e.waybillStatus(no); st != "voided" {
		t.Errorf("拆完之后父单状态=%q，应为 voided", st)
	}
}

func (e *testEnv) waybillStatus(no string) (string, error) {
	var s string
	err := e.pool.QueryRow(context.Background(),
		`SELECT status FROM ops_waybill WHERE waybill_no=$1`, no).Scan(&s)
	return s, err
}

// TestWaybillMergeSumsCargo 合单的货量要等于各源单之和。
//
// 这一条目前是对的（合单 SQL 里就是 sum(...)），钉住是因为它和拆单
// 是同一件事的两半：拆单那半原先两个方向都能穿过去。
func TestWaybillMergeSumsCargo(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	a := e.mkWaybillAt("pending_dispatch")
	b := e.mkWaybillAt("pending_dispatch")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`UPDATE ops_waybill SET cargo_quantity=3, cargo_weight_ton=1.5, cargo_volume_cbm=4 WHERE waybill_no=$1`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE ops_waybill SET cargo_quantity=7, cargo_weight_ton=2.5, cargo_volume_cbm=6 WHERE waybill_no=$1`, b); err != nil {
		t.Fatal(err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_waybill WHERE waybill_no LIKE $1 OR waybill_no LIKE $2`,
			a+"-M%", b+"-M%")
	})

	rec := e.call(token, "POST", "/api/v1/waybills/merge",
		fmt.Sprintf(`{"waybill_nos":["%s","%s"]}`, a, b))
	if rec.Code >= 400 {
		t.Fatalf("合单返回 %d：%s", rec.Code, rec.Body.String())
	}
	var q int
	var wt, v string
	if err := e.pool.QueryRow(ctx, `
		SELECT cargo_quantity, cargo_weight_ton::text, cargo_volume_cbm::text
		FROM ops_waybill WHERE waybill_no LIKE $1 OR waybill_no LIKE $2
		ORDER BY created_at DESC LIMIT 1`, a+"-M%", b+"-M%").Scan(&q, &wt, &v); err != nil {
		t.Fatalf("查合并单失败：%v", err)
	}
	if q != 10 || wt != "4.00" || v != "10.00" {
		t.Errorf("合并单 %d 件 / %s 吨 / %s 方，应为 10 / 4.00 / 10.00", q, wt, v)
	}
}
