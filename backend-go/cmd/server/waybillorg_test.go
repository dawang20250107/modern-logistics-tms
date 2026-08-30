package main

// 派出去的运单要落在建单那个网点名下。
//
// 由来：拿演示客服（数据范围 org=上海）真的登进去点，
// 它在「订单管理」里看得见自己建的单，点开对应的运单却是
// **「运单不存在、无权访问或数据暂时不可用」**加一颗重试按钮。
//
// 根因：`ops_waybill.organization_id` 决定运单的数据范围，
// 而主派单路径（dispatch.go）和批量派承运商（batchdispatch.go）
// **压根没写这一列**——落库就是 NULL，而 NULL 只有 all 档看得见。
// 转承运那条路径（quotes.go）倒是写了，取的是建单人的组织。
// 同一个字段，三条路径两种行为，其中两条产出的运单谁都看不见。
//
// 归属取**建单人的组织**而不是派单人的：单子是哪个网点接的，
// 执行过程就该由那个网点看得见。中心调度替各网点派单是常态，
// 按派单人算的话，网点会在派单那一刻失去自己单子的可见性。
// （这也是 quotes.go 已经选定的语义，三条路径统一到它。）

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// mkOrgUser 建一个挂在指定组织下、数据范围为 org 的账号。
func (e *testEnv) mkOrgUser(orgID string, perms ...string) string {
	e.t.Helper()
	tok := e.mkUserScoped(false, "org", perms...)
	ctx := context.Background()
	// mkUserScoped 建的账号没有组织归属，这里补上
	if _, err := e.pool.Exec(ctx, `
		UPDATE accounts_user SET organization_id=$2::uuid
		WHERE id = (SELECT user_id FROM iam_role_assignment ORDER BY created_at DESC LIMIT 1)`,
		nil, orgID); err != nil {
		e.t.Fatalf("挂组织失败：%v", err)
	}
	return tok
}

func (e *testEnv) mkOrg() string {
	e.t.Helper()
	id := uuid.NewString()
	// NOT NULL 的列一个都不能少（这张表上有二十来个），少一个就是建不出来
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO iam_organization (id, created_at, updated_at, code, name, type, path, is_active,
		  parent_id, address, business_phone, city, complaint_phone, district, manager_name,
		  manager_phone, org_property, province, receipt_return_address, service_phone,
		  short_name, sort_order)
		VALUES ($1::uuid, now(), now(), $2, '运单归属用例网点', 'branch', $2, true,
		  NULL, '', '', '', '', '', '', '', '', '', '', '', '', 0)`,
		id, "WBORG"+id[:8]); err != nil {
		e.t.Fatalf("建组织失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM iam_organization WHERE id=$1::uuid`, id)
	})
	return id
}

// TestDispatchedWaybillKeepsOrderOrganization 派单产出的运单要带上建单网点。
func TestDispatchedWaybillKeepsOrderOrganization(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	su := e.mkUser(true)
	org := e.mkOrg()

	// 建单人挂在这个网点下
	var creator string
	if err := e.pool.QueryRow(ctx, `
		SELECT id::text FROM accounts_user WHERE username LIKE 'authz_test_%'
		ORDER BY date_joined DESC LIMIT 1`).Scan(&creator); err != nil {
		t.Fatalf("找不到测试账号：%v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE accounts_user SET organization_id=$2::uuid WHERE id=$1::uuid`, creator, org); err != nil {
		t.Fatalf("给建单人挂组织失败：%v", err)
	}

	rec := e.call(su, "POST", "/api/v1/orders/intake",
		`{"fields":{"customer_name":"运单归属用例","contact_phone":"13800000000","origin":"上海",
			"destination":"杭州","cargo_name":"日用品","weight":"2","quantity":"5"},"status":"pooled"}`)
	if rec.Code >= 400 {
		t.Fatalf("建单失败 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID      string `json:"id"`
			OrderNo string `json:"order_no"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	// 建单人就是那个挂了组织的账号（intake 用的是 su 的身份），统一改成它
	if _, err := e.pool.Exec(ctx,
		`UPDATE ops_order SET created_by_id=$2::uuid WHERE id=$1::uuid`, out.Data.ID, creator); err != nil {
		t.Fatalf("改建单人失败：%v", err)
	}
	e.dropOrder(out.Data.OrderNo)

	// 派单（自有车）
	// 自己造一台车，不要去抢库里的：演示库的 6 台车全在跑，
	// 直接取第一台会拿到 409 VEHICLE_BUSY —— 那是**正确行为**，
	// 却会让这条用例红在一个跟它无关的原因上。
	vehID := uuid.NewString()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO md_vehicle (id, created_at, updated_at, is_deleted, plate_no, vehicle_type,
		  ownership_type, is_active, road_transport_cert_no, load_capacity_ton, volume_capacity_cbm,
		  vehicle_class, dispatch_source, body_type, vehicle_length_m)
		VALUES ($1::uuid, now(), now(), false, $2, '厢式货车', 'own', true, '', 10, 40,
		  'medium', 'own', 'van', 6.8)`, vehID, "用例"+vehID[:6]); err != nil {
		t.Fatalf("造车失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM md_vehicle WHERE id=$1::uuid`, vehID)
	})
	if code := e.call(su, "POST", "/api/v1/orders/"+out.Data.ID+"/claim", `{}`).Code; code >= 400 && code != http.StatusConflict {
		t.Fatalf("锁定失败 %d", code)
	}
	dr := e.call(su, "POST", "/api/v1/orders/"+out.Data.ID+"/dispatch",
		`{"dispatch_type":"own_vehicle","vehicle":"`+vehID+`"}`)
	if dr.Code >= 400 {
		t.Fatalf("派单失败 %d：%s", dr.Code, dr.Body.String())
	}

	var wbNo string
	var wbOrg *string
	if err := e.pool.QueryRow(ctx, `
		SELECT waybill_no, organization_id::text FROM ops_waybill WHERE order_id=$1::uuid
		ORDER BY created_at DESC LIMIT 1`, out.Data.ID).Scan(&wbNo, &wbOrg); err != nil {
		t.Fatalf("找不到派出来的运单：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			`DELETE FROM ops_waybill_event WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, wbNo)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM ops_waybill WHERE waybill_no=$1`, wbNo)
	})

	if wbOrg == nil {
		t.Fatalf("运单 %s 的 organization_id 是 NULL —— 数据范围按这一列算，"+
			"NULL 只有「全部」档看得见。\n"+
			"  后果：单子是哪个网点接的，那个网点在派单那一刻就失去了它的可见性——"+
			"订单列表里看得见，点开运单是「运单不存在、无权访问」。", wbNo)
	}
	if *wbOrg != org {
		t.Errorf("运单归属是 %s，期望建单网点 %s", *wbOrg, org)
	}
}
