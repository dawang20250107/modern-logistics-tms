package main

// 拆单：把一张订单的货物明细分到几张子订单上，原单作废。
//
// 这是「货」这条线上最容易悄悄丢东西的操作：明细行被搬到别处，
// 原单随即作废。搬漏一行，那批货就留在一张已作废的订单上——
// 不报错、不提示，只是从流程里消失了。
//
// 这条路径此前一个用例都没有。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// mkOrderWithItems 造一张带 n 项货物明细的订单，返回订单 id 与明细 id 列表。
func (e *testEnv) mkOrderWithItems(n int) (orderID string, itemIDs []string) {
	e.t.Helper()
	orderID = e.mkOrder()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		iid := uuid.NewString()
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO ops_order_cargo_item (id, created_at, updated_at, seq, name, quantity,
			  weight_ton, volume_cbm, package_type, temperature_range, remark, order_id)
			VALUES ($1::uuid, now(), now(), $2, $3, $4, $5, $6, '纸箱', '', '', $7::uuid)`,
			iid, i+1, fmt.Sprintf("货物%d", i+1), (i+1)*10, (i+1)*2, (i+1)*3, orderID); err != nil {
			e.t.Fatalf("造货物明细失败：%v", err)
		}
		itemIDs = append(itemIDs, iid)
	}
	// 让订单头部的合计与明细一致
	if _, err := e.pool.Exec(ctx, `
		UPDATE ops_order o SET cargo_quantity=t.q, cargo_weight_ton=t.w, cargo_volume_cbm=t.v
		FROM (SELECT sum(quantity) q, sum(weight_ton) w, sum(volume_cbm) v
		      FROM ops_order_cargo_item WHERE order_id=$1::uuid) t
		WHERE o.id=$1::uuid`, orderID); err != nil {
		e.t.Fatalf("回填合计失败：%v", err)
	}
	e.t.Cleanup(func() {
		// 拆出来的子订单只能顺着明细找回来（表上没有父子列）
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_order WHERE id IN
			(SELECT DISTINCT order_id FROM ops_order_cargo_item WHERE id::text = ANY($1))
			AND id <> $2::uuid`, itemIDs, orderID)
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_order_cargo_item WHERE id::text = ANY($1)`, itemIDs)
	})
	return orderID, itemIDs
}

// liveCargo 统计这批明细里「还挂在有效订单上」的货量。
//
// 按明细 id 统计而不是按订单树：ops_order 上没有父子列，
// 父子关系只存在于事件日志里。而要问的问题本来就是明细级的——
// 这几行货，拆完之后还在不在流程里。
func (e *testEnv) liveCargo(itemIDs []string) (qty int, weight string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), `
		SELECT COALESCE(sum(ci.quantity),0), COALESCE(sum(ci.weight_ton),0)::text
		FROM ops_order_cargo_item ci JOIN ops_order o ON o.id = ci.order_id
		WHERE ci.id::text = ANY($1)
		  AND o.status NOT IN ('cancelled','voided') AND NOT o.is_deleted`, itemIDs).
		Scan(&qty, &weight); err != nil {
		e.t.Fatalf("统计货量失败：%v", err)
	}
	return qty, weight
}

func (e *testEnv) split(token, orderID string, groups [][]string) *httpRecLike {
	e.t.Helper()
	gs := make([]string, len(groups))
	for i, g := range groups {
		gs[i] = `{"cargo_item_ids":["` + strings.Join(g, `","`) + `"]}`
	}
	body := `{"groups":[` + strings.Join(gs, ",") + `]}`
	rec := e.call(token, "POST", "/api/v1/orders/"+orderID+"/split", body)
	return &httpRecLike{Code: rec.Code, Body: rec.Body.String()}
}

type httpRecLike struct {
	Code int
	Body string
}

// TestSplitKeepsAllCargo 拆完之后，货不能少。
//
// 拆单会把明细搬到子订单、然后把原单作废。没被任何一组选中的明细
// 会留在那张已作废的原单上——从流程里消失，而且不报错。
// 3 项拆成 2 组只覆盖 2 项，第 3 项就是这么没的。
func TestSplitKeepsAllCargo(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	orderID, items := e.mkOrderWithItems(3)

	beforeQty, beforeW := e.liveCargo(items)
	if beforeQty != 60 { // 10+20+30
		t.Fatalf("前提不成立：拆前货量 %d，期望 60", beforeQty)
	}

	// 故意只覆盖前两项——第三项没人要
	rec := e.split(token, orderID, [][]string{{items[0]}, {items[1]}})
	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		afterQty, afterW := e.liveCargo(items)
		if afterQty != beforeQty {
			t.Errorf("拆单前有效货量 %d 件 / %s 吨，拆完只剩 %d 件 / %s 吨 —— "+
				"没被分组选中的明细留在了已作废的原单上，货从流程里消失了，而接口回的是成功",
				beforeQty, beforeW, afterQty, afterW)
		}
		return
	}
	// 也可以选择直接拒绝这种拆法——那同样是对的，只要别默默丢货
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusConflict {
		t.Errorf("拆单返回 %d：%s", rec.Code, rec.Body)
	}
}

// TestSplitAllItemsConserved 正常拆法（覆盖全部明细）必须守恒。
func TestSplitAllItemsConserved(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	orderID, items := e.mkOrderWithItems(3)
	beforeQty, beforeW := e.liveCargo(items)

	rec := e.split(token, orderID, [][]string{{items[0], items[1]}, {items[2]}})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("拆单返回 %d：%s", rec.Code, rec.Body)
	}
	afterQty, afterW := e.liveCargo(items)
	if afterQty != beforeQty || afterW != beforeW {
		t.Errorf("拆单前 %d 件 / %s 吨，拆后 %d 件 / %s 吨 —— 货量不守恒",
			beforeQty, beforeW, afterQty, afterW)
	}

	// 子订单头部的合计要和它自己的明细对得上
	rows, err := e.pool.Query(context.Background(), `
		SELECT o.order_no, o.cargo_quantity, COALESCE(sum(ci.quantity),0)
		FROM ops_order o JOIN ops_order_cargo_item ci ON ci.order_id = o.id
		WHERE o.id <> $2::uuid AND ci.id::text = ANY($1)
		GROUP BY o.id, o.order_no, o.cargo_quantity`, items, orderID)
	if err != nil {
		t.Fatalf("查子订单失败：%v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var no string
		var head, detail int
		if err := rows.Scan(&no, &head, &detail); err != nil {
			t.Fatal(err)
		}
		n++
		if head != detail {
			t.Errorf("子订单 %s 头部写着 %d 件，明细合计是 %d 件", no, head, detail)
		}
	}
	if n != 2 {
		t.Errorf("应拆出 2 张子订单，实际 %d 张", n)
	}
}

// TestMergeSumsHeaderCargoWhenNoItemRows 合单：没有明细行的订单，货量也要合起来。
//
// 库里 5 万单里只有 28 单有货物明细行——绝大多数订单的货量只写在表头
// （cargo_quantity / cargo_weight_ton）。而合单后的重算只在"有明细行"时才执行
// （recomputeCargo 带着 EXISTS 条件），没有明细行时新单的表头是从第一张源单
// **整列复制**来的：合并 A(1 件) 和 B(1 件)，得到的新单写着 1 件。
// 一半的货在合单这一步凭空没了，而且是最常见的那种订单。
func TestMergeSumsHeaderCargoWhenNoItemRows(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	a := e.mkOrder()
	b := e.mkOrder()
	ctx := context.Background()
	// 给两张单不同的表头货量，好看出是"求和"还是"照抄第一张"
	if _, err := e.pool.Exec(ctx, `
		UPDATE ops_order SET cargo_quantity=$2, cargo_weight_ton=$3 WHERE id=$1::uuid`, a, 3, 1.5); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `
		UPDATE ops_order SET cargo_quantity=$2, cargo_weight_ton=$3 WHERE id=$1::uuid`, b, 7, 2.5); err != nil {
		t.Fatal(err)
	}

	rec := e.call(token, "POST", "/api/v1/orders/merge", `{"ids":["`+a+`","`+b+`"]}`)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("合单返回 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Quantity int    `json:"cargo_quantity"`
			Weight   string `json:"cargo_weight_ton"` // DRF 的 Decimal 序列化成字符串
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v — %s", err, rec.Body.String())
	}
	merged := out.Data
	if merged.ID == "" {
		t.Fatalf("合单没有返回新单：%s", rec.Body.String())
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM ops_order WHERE id=$1::uuid`, merged.ID)
	})
	if merged.Quantity != 10 {
		t.Errorf("合单后件数 %d，应为 3+7=10 —— 没有明细行的订单，货量在合单这一步丢了",
			merged.Quantity)
	}
	if merged.Weight != "4.00" {
		t.Errorf("合单后吨数 %s，应为 1.5+2.5=4.00", merged.Weight)
	}
}
