package main

// 承运合同的按需生成。
//
// 前端 WaybillDetailPage 里写好了 genContract（POST /waybills/{no}/contract）、
// sendContract、confirmContract 三个 mutation，但一个都没被渲染出来；
// 后端那条路由此前也只注册了 GET。两边各缺一半，
// 「承运合同」这一整段从界面上完全够不着。
//
// 合同本身是有的：派单事务里会自动生成一份。但派单时没司机的运单
// （dispatch_status=pending_driver_submit，后补司机是常规操作）永远没有合同，
// 于是「发送给司机」「司机确认」这两步也永远走不到——工作流面板上
// 「承运合同 未生成」会一直挂着，而界面上没有任何东西能改变它。
//
// 合同是承运责任、运费金额、异常责任的书面依据。司机跑了一趟没有合同，
// 出事时双方各说各话。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mkWaybillWithDriver 造一张有司机、无合同的运单，返回运单号。
func (e *testEnv) mkWaybillWithDriver() string {
	e.t.Helper()
	no := e.mkWaybillAt("dispatched")
	phone, _ := e.mkDriver()
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE ops_waybill SET driver_id = (SELECT id FROM md_driver WHERE phone=$2)
		WHERE waybill_no=$1`, no, phone); err != nil {
		e.t.Fatalf("挂司机失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			`DELETE FROM ops_contract WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, no)
	})
	return no
}

func (e *testEnv) contractOf(token, no string) (map[string]any, int) {
	e.t.Helper()
	rec := e.call(token, "GET", "/api/v1/waybills/"+no+"/contract", "")
	var out struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Data, rec.Code
}

// TestGenerateContractOnDemand 按需生成：页面上那颗按钮必须真的能用。
func TestGenerateContractOnDemand(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillWithDriver()

	if c, _ := e.contractOf(token, no); c != nil {
		t.Fatalf("前提不成立：这张运单已经有合同了")
	}

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("生成合同返回 %d：%s", rec.Code, rec.Body.String())
	}

	c, _ := e.contractOf(token, no)
	if c == nil {
		t.Fatal("生成之后仍然查不到合同")
	}
	// 合同正文里必须有能对上账的东西：合同号、运单号、司机名。
	// 只断言"生成了一条记录"是不够的——内容空着的合同和没有合同一样没用。
	content, _ := c["content"].(string)
	for _, want := range []string{"运输承运合同", no} {
		if !strings.Contains(content, want) {
			t.Errorf("合同正文里没有 %q：\n%s", want, content)
		}
	}
	if s, _ := c["contract_no"].(string); !strings.HasPrefix(s, "HT") {
		t.Errorf("合同编号不对：%q", s)
	}
	if s, _ := c["confirm_status"].(string); s != "pending" {
		t.Errorf("新合同应是待发送，实际 %q", s)
	}
}

// TestGenerateContractNeedsDriver 没司机就没合同——不能生成一份承运人空着的合同。
func TestGenerateContractNeedsDriver(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillAt("dispatched") // 没挂司机

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("没司机时应 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "司机") {
		t.Errorf("报错没说清是缺司机：%s", rec.Body.String())
	}
}

// TestGenerateContractWontOverwriteConfirmed 已确认的合同不能被一键覆盖。
//
// 司机确认过的那份是双方已经达成的约定。允许悄悄换掉它，等于给了
// 「先让司机签，再改运费」这条路。换司机要重出合同是合理需求，
// 但前提是那份还没被确认。
func TestGenerateContractWontOverwriteConfirmed(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillWithDriver()

	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract", `{}`); rec.Code != http.StatusCreated {
		t.Fatalf("首次生成失败 %d：%s", rec.Code, rec.Body.String())
	}
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE ops_contract SET confirm_status='confirmed', confirmed_at=now()
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, no); err != nil {
		t.Fatalf("置为已确认失败：%v", err)
	}

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract", `{}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("已确认的合同被重新生成了（%d）—— 等于允许先让司机签、再改条款", rec.Code)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM ops_contract
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, no).Scan(&n)
	if n != 1 {
		t.Errorf("合同条数变成了 %d，应保持 1", n)
	}
}

// TestContractConfirmCannotBeFlippedAfterConfirmed 已确认的合同不能被改判成拒签。
//
// 确认这一步没有任何状态守卫：谁在什么时候点「司机拒签」，
// 都会把一份**司机已经确认过**的合同改写成"已拒签"，并顺手重写 confirmed_at。
// 承运合同是出事时双方唯一的书面依据，"他签过"这件事不该被后来的一次点击抹掉。
func TestContractConfirmCannotBeFlippedAfterConfirmed(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillWithDriver()

	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract", `{}`); rec.Code != http.StatusCreated {
		t.Fatalf("生成合同失败 %d", rec.Code)
	}
	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract/send", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("发送失败 %d", rec.Code)
	}
	if rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract/confirm",
		`{"accepted":true,"reply":"同意承运"}`); rec.Code != http.StatusOK {
		t.Fatalf("确认失败 %d", rec.Code)
	}

	rec := e.call(token, "POST", "/api/v1/waybills/"+no+"/contract/confirm",
		`{"accepted":false,"reply":"改主意了"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("把已确认的合同改判成拒签，返回 %d（应 409）—— "+
			"「司机签过」这件事被一次点击抹掉了", rec.Code)
	}
	c, _ := e.contractOf(token, no)
	if s, _ := c["confirm_status"].(string); s != "confirmed" {
		t.Errorf("合同状态被改成了 %q", s)
	}
}

// TestContractConfirmIsIdempotent 重复确认不改动已经记下的时间。
func TestContractConfirmIsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	no := e.mkWaybillWithDriver()
	_ = e.call(token, "POST", "/api/v1/waybills/"+no+"/contract", `{}`)
	_ = e.call(token, "POST", "/api/v1/waybills/"+no+"/contract/send", `{}`)
	_ = e.call(token, "POST", "/api/v1/waybills/"+no+"/contract/confirm", `{"accepted":false,"reply":"车坏了"}`)

	var first time.Time
	if err := e.pool.QueryRow(context.Background(), `
		SELECT confirmed_at FROM ops_contract
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)
		ORDER BY created_at DESC LIMIT 1`, no).Scan(&first); err != nil {
		t.Fatalf("查确认时间失败：%v", err)
	}
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE ops_contract SET confirmed_at = confirmed_at - interval '1 hour'
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)`, no); err != nil {
		t.Fatalf("调整时间失败：%v", err)
	}
	var before time.Time
	_ = e.pool.QueryRow(context.Background(), `
		SELECT confirmed_at FROM ops_contract
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)
		ORDER BY created_at DESC LIMIT 1`, no).Scan(&before)

	// 再拒签一次：结果一样，就不该改动已经记下的时间
	_ = e.call(token, "POST", "/api/v1/waybills/"+no+"/contract/confirm", `{"accepted":false,"reply":"车坏了"}`)
	var after time.Time
	_ = e.pool.QueryRow(context.Background(), `
		SELECT confirmed_at FROM ops_contract
		WHERE waybill_id IN (SELECT id FROM ops_waybill WHERE waybill_no=$1)
		ORDER BY created_at DESC LIMIT 1`, no).Scan(&after)
	if !after.Equal(before) {
		t.Errorf("重复拒签把时间从 %v 改成了 %v", before, after)
	}
}
