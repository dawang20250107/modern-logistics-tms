package main

// 位置数据不能由任意登录用户写入。
//
// 车辆遥测和运单轨迹点这两条**本来是给车载终端和网关用的**，前端一次都不调。
// 可它们挂在「登录后」这一组上，而函数体里一句权限校验都没有——
// 实测**零权限的登录账号**能给任意车牌、任意运单灌位置点，
// 两次都返回 202 并真的入库。
//
// 后果不是"多了几条脏数据"：轨迹是超速告警、围栏告警、ETA 的输入，
// 也是**异常定责时的证据**。谁都能往里写，那几样就都不作数了。
//
// 更值得记的是：authzcoverage_test.go 的豁免名单里，这两条当时写的理由是
// 「设备侧：走设备/网关凭据，不走用户权限」——**那句话与事实不符**。
// 豁免名单是靠理由撑着的，写错一条理由，那条路由就再也不会被任何检查看一眼。
// 所以理由必须是核对过的事实，不是印象。
//
// 该有的机制是设备凭据（库里 iam_api_key 那张表就是为它准备的，
// 但代码里一处都没用上）。发布前没有已知的终端对接方，先按权限点挡住，
// 这一条已写进交付说明的"仍然待办"。

import (
	"context"
	"net/http"
	"testing"
)

func TestTelemetryIngestNeedsPermission(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	var plate string
	if err := e.pool.QueryRow(ctx,
		`SELECT plate_no FROM md_vehicle WHERE NOT is_deleted LIMIT 1`).Scan(&plate); err != nil {
		t.Skipf("库里没有车辆，这条用例测不到东西：%v", err)
	}
	telemetry := `{"reports":[{"vehicle_plate":"` + plate + `","lng":121.5,"lat":31.2,` +
		`"speed":120,"reported_at":"2026-08-30T10:00:00Z"}]}`
	points := `{"points":[{"waybill_no":"NOSUCH","lng":121.5,"lat":31.2,` +
		`"reported_at":"2026-08-30T10:00:00Z"}]}`

	for _, c := range []struct {
		who   string
		token string
	}{
		{"零权限账号", e.mkUser(false)},
		{"只有 masterdata.view 的账号", e.mkUser(false, "masterdata.view")},
		{"只有 telematics.view 的账号（演示调度员就是这档）", e.mkUser(false, "telematics.view")},
	} {
		for _, ep := range []struct{ path, body, what string }{
			{"/api/v1/telematics/ingest", telemetry, "车辆遥测"},
			{"/api/v1/tracking/points", points, "运单轨迹点"},
		} {
			code := e.call(c.token, "POST", ep.path, ep.body).Code
			if code != http.StatusForbidden {
				t.Errorf("%s 往「%s」灌数据拿到 %d，应该是 403。\n"+
					"  轨迹是超速告警、围栏告警、ETA 的输入，也是异常定责时的证据——"+
					"谁都能写，那几样就都不作数了。", c.who, ep.what, code)
			}
		}
	}

	// 有权限的要放行，别把真正的接入方也挡了
	ok := e.mkUser(false, "telematics.manage")
	for _, ep := range []struct{ path, body, what string }{
		{"/api/v1/telematics/ingest", telemetry, "车辆遥测"},
		{"/api/v1/tracking/points", points, "运单轨迹点"},
	} {
		if code := e.call(ok, "POST", ep.path, ep.body).Code; code == http.StatusForbidden {
			t.Errorf("有 telematics.manage 的账号往「%s」上报反被 403 —— 闸挂错了权限点", ep.what)
		}
	}

	// 不带令牌仍然是 401：这条本来就成立，一并钉住
	if code := e.call("", "POST", "/api/v1/telematics/ingest", telemetry).Code; code != http.StatusUnauthorized {
		t.Errorf("不带令牌上报拿到 %d，应该是 401", code)
	}
}
