package main

// 代收货款（COD）：司机替发货方向收货人收现金，再回款给平台。
//
// 这条线上唯一的凭据是两个时间戳：什么时候收的、什么时候回的。
// 出现现金纠纷时，能拿出来的就是它们。

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (e *testEnv) mkCODWaybill(amount string) string {
	e.t.Helper()
	no := e.mkWaybillAt("arrived")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE ops_waybill SET cod_amount=$2::numeric, cod_status='pending' WHERE waybill_no=$1`,
		no, amount); err != nil {
		e.t.Fatalf("设代收金额失败：%v", err)
	}
	return no
}

func (e *testEnv) codRow(no string) (status string, collectedAt *time.Time) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT cod_status, cod_collected_at FROM ops_waybill WHERE waybill_no=$1`, no).
		Scan(&status, &collectedAt); err != nil {
		e.t.Fatalf("查代收状态失败：%v", err)
	}
	return status, collectedAt
}

// TestCODCollectThenRemit 正常路径：代收 → 回款。
func TestCODCollectThenRemit(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkCODWaybill("1200.00")

	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/collect-cod", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("代收返回 %d：%s", rec.Code, rec.Body.String())
	}
	if st, at := e.codRow(no); st != "collected" || at == nil {
		t.Fatalf("代收后状态=%q 时间=%v", st, at)
	}
	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/remit-cod", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("回款返回 %d：%s", rec.Code, rec.Body.String())
	}
	if st, _ := e.codRow(no); st != "remitted" {
		t.Errorf("回款后状态=%q", st)
	}
}

// TestCODRepeatCollectKeepsFirstTime 重复点「已代收」不能把代收时间改晚。
//
// 原实现只挡了"已回款不能再代收"，没挡"已代收再代收"：第二次点会重写
// cod_collected_at。司机 10:00 收的现金，下午有人手滑再点一次，
// 记录就变成 15:00 —— 而这条时间戳正是现金纠纷时唯一能拿出来的东西。
// 顺带还会在时间线上多出一条「已代收」，看起来像收了两次。
func TestCODRepeatCollectKeepsFirstTime(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkCODWaybill("800.00")

	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/collect-cod", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("首次代收返回 %d", rec.Code)
	}
	_, first := e.codRow(no)
	if first == nil {
		t.Fatal("首次代收没有写时间")
	}

	// 把时间往前挪一小时，好让"被重写"这件事看得出来
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE ops_waybill SET cod_collected_at = cod_collected_at - interval '1 hour'
		 WHERE waybill_no=$1`, no); err != nil {
		t.Fatalf("调整时间失败：%v", err)
	}
	_, before := e.codRow(no)

	// 再点一次：可以是成功（幂等），但不能改动已经记下的事实
	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/collect-cod", `{}`)
	if rec.Code >= 500 {
		t.Fatalf("重复代收返回 %d：%s", rec.Code, rec.Body.String())
	}
	_, after := e.codRow(no)
	if after == nil || !after.Equal(*before) {
		t.Errorf("重复点「已代收」把代收时间从 %v 改成了 %v —— "+
			"现金纠纷时这条时间戳是唯一的凭据，不能被后来的一次误点改写", before, after)
	}

	// 时间线上也不该多出第二条「已代收」
	var n int
	_ = e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM ops_waybill_event
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)
		  AND event_type='cod_collected'`, no).Scan(&n)
	if n > 1 {
		t.Errorf("时间线上有 %d 条「已代收」——看起来像收了两次现金", n)
	}
}

// TestCODRemitBeforeCollectRejected 没收到钱不能回款。
func TestCODRemitBeforeCollectRejected(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkCODWaybill("500.00")

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/remit-cod", `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("未代收就回款应 409，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "还没有确认代收") {
		t.Errorf("报错没说清是缺代收这一步：%s", rec.Body.String())
	}
}

// TestCODRemitTwiceSaysSo 重复回款要说"已经回过了"，不能说成"还没代收"。
//
// 回款不做幂等——重复回款是真金白银，必须挡下来。但挡的时候要把话说对：
// 原先两种情况共用一句「仅已代收的货款可回款」，操作员看到会以为漏了
// 代收那一步，回头去点「已代收」，而钱其实早就回过了。
func TestCODRemitTwiceSaysSo(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkCODWaybill("300.00")

	_ = e.call(token, "POST", "/api/v1/waybills/"+no+"/collect-cod", `{}`)
	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/remit-cod", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("首次回款失败 %d：%s", rec.Code, rec.Body.String())
	}
	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/remit-cod", `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("重复回款应 409，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "已经回款过") {
		t.Errorf("重复回款的报错说成了别的意思：%s", rec.Body.String())
	}
}
