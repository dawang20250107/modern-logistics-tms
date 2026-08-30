package main

// 演示的「客服」角色要能干客服的活。
//
// 交付说明第七节那条验收链，第一步写的就是「客服工作台建一单」。
// 而演示库里的客服角色只有 waybill.view + masterdata.view，
// 实测 POST /orders/intake 返回 403 ——**文档写的验收链，
// 用文档给的角色跑不通**。客服工作台还挂在侧栏上，点得进去、
// 表单能填满，最后在提交那一下才告诉你"缺少所需权限"。
//
// 直接把 waybill.manage 给客服是不行的：那一并给出去的是派单、签收、
// 代收货款打款、异常定责赔钱——正是这个 PR 前面花了大力气挡住的东西。
// 所以单列 waybill.create，只管"把单子录进来"。
//
// 这条用例把两个方向都钉住：建单要能成，越权的动作仍要 403。

import (
	"context"
	"net/http"
	"testing"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
)

// csPerms 与 cmd/seed 里 SEED_CS 的权限点逐字一致。
// 改了那边不改这边，用例会红——这正是想要的：演示角色能干什么
// 是交付说明里写着的事，不该悄悄漂移。
var csPerms = []string{"waybill.view", "waybill.create", "masterdata.view"}

func TestSeedCsRoleCanCreateOrdersButNotDispatch(t *testing.T) {
	e := newTestEnv(t)
	cs := e.mkUser(false, csPerms...)

	t.Run("能建单", func(t *testing.T) {
		body := `{"fields":{"customer_name":"客服角色用例","contact_phone":"13800000000",
			"origin":"上海","destination":"杭州","cargo_name":"日用品",
			"weight":"3.5","quantity":"10"}}`
		rec := e.call(cs, "POST", "/api/v1/orders/intake", body)
		if rec.Code >= 400 {
			t.Fatalf("客服建单返回 %d：%s\n"+
				"  交付说明第七节验收链第一步就是这个动作。", rec.Code, rec.Body.String())
		}
		t.Cleanup(func() {
			_, _ = e.pool.Exec(context.Background(), `
				DELETE FROM ops_order_event WHERE order_id IN
				  (SELECT id FROM ops_order WHERE customer_name='客服角色用例')`)
			_, _ = e.pool.Exec(context.Background(),
				`DELETE FROM ops_order WHERE customer_name='客服角色用例'`)
		})
	})

	// 建单的门低了，别的门不能跟着低。
	for _, c := range []struct{ path, body, what string }{
		{"/api/v1/orders/assign", `{"order_ids":[],"dispatcher":""}`, "指派调度"},
		{"/api/v1/orders/" + zeroUUID + "/dispatch", `{}`, "派单"},
		{"/api/v1/orders/merge", `{"order_ids":[]}`, "合单"},
		{"/api/v1/waybills/NOSUCH/sign", `{}`, "签收"},
		{"/api/v1/exceptions/" + zeroUUID + "/close", `{"amount":"800"}`, "定责关闭异常"},
	} {
		if code := e.call(cs, "POST", c.path, c.body).Code; code != http.StatusForbidden {
			t.Errorf("客服做「%s」拿到 %d，应该是 403 —— "+
				"waybill.create 只该管把单子录进来", c.what, code)
		}
	}
}

// TestWaybillCreateIsGrantable 新加的权限点必须在目录里，否则勾不上。
//
// 和 org.employee 那次是同一个坑：代码里校验、目录里没有，
// 于是任何角色都授不了它——加了跟没加一样。
func TestWaybillCreateIsGrantable(t *testing.T) {
	for _, p := range auth.Catalog {
		if p.Code == "waybill.create" {
			if p.Name == "" || p.Module != "waybill" {
				t.Errorf("waybill.create 的目录行不完整：%+v", p)
			}
			return
		}
	}
	t.Error("waybill.create 不在 auth.Catalog 里 —— 没有目录行就没有勾选框，任何角色都授不了它")
}

// TestWaybillManageStillCoversCreate 只勾了 manage 的角色不能因此建不了单。
func TestWaybillManageStillCoversCreate(t *testing.T) {
	e := newTestEnv(t)
	// 演示的调度员就是这么配的：有 manage，没有 create
	d := e.mkUser(false, "waybill.view", "waybill.manage", "masterdata.view")
	body := `{"fields":{"customer_name":"调度建单用例","contact_phone":"13800000000",
		"origin":"上海","destination":"宁波","cargo_name":"日用品","weight":"2","quantity":"5"}}`
	rec := e.call(d, "POST", "/api/v1/orders/intake", body)
	if rec.Code >= 400 {
		t.Fatalf("只勾了 waybill.manage 的角色建单返回 %d：%s\n"+
			"  拆出 waybill.create 不能让原本能建单的角色建不了。", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `
			DELETE FROM ops_order_event WHERE order_id IN
			  (SELECT id FROM ops_order WHERE customer_name='调度建单用例')`)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM ops_order WHERE customer_name='调度建单用例'`)
	})
}
