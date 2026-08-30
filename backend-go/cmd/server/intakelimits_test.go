package main

// 填得太长的字段，要说清是哪一栏。
//
// 由来：给「客服」角色补上建单权限之后，第一次真的把这颗按钮按下去，
// 拿到的是 **HTTP 500 + 一句原始 Postgres 报错**：
//
//	建单失败：ERROR: value too long for type character varying(32) (SQLSTATE 22001)
//
// 七个字段试下来全是这一句：联系电话、包装、温区、提货联系电话……
// 共同点是**它不说是哪个字段**。客服粘一段长地址或长包装说明进来就撞上，
// 只能去问技术。而这是下单，是整条链的第一步。
//
// 三件事一起改：
//   · 建单前按 information_schema 里的真实列长挡一道（不硬编码，列改宽了
//     校验跟着改），报错点名字段并给出上限和实际长度
//   · 那是用户填出来的，所以是 400 不是 500 —— 500 会被监控当成服务端故障
//   · 万一还是撞了库的约束，原文只进日志，回给前端的是能照着做的话

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type intakeRec struct {
	Code int
	Body string
}

func (e *testEnv) intake(token string, extra map[string]string) *intakeRec {
	e.t.Helper()
	f := map[string]string{
		"customer_name": "超长字段用例", "contact_phone": "13800000000",
		"origin": "上海", "destination": "杭州", "cargo_name": "日用品",
		"weight": "1", "quantity": "1",
	}
	for k, v := range extra {
		f[k] = v
	}
	parts := make([]string, 0, len(f))
	for k, v := range f {
		parts = append(parts, fmt.Sprintf("%q:%q", k, v))
	}
	rec := e.call(token, "POST", "/api/v1/orders/intake",
		`{"fields":{`+strings.Join(parts, ",")+`}}`)
	return &intakeRec{Code: rec.Code, Body: rec.Body.String()}
}

// orderNo 从返回体里取新建单号——ops_order 上没有 customer_name 这一列，
// 拿它做清理条件是删不掉的（而 `_, _ =` 会把这件事吞掉，垃圾就这么攒起来）。
func (r *intakeRec) orderNo() string {
	var out struct {
		Data struct {
			OrderNo string `json:"order_no"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(r.Body), &out)
	return out.Data.OrderNo
}

func (r *intakeRec) errCode() string {
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(r.Body), &out)
	return out.Error.Code
}

func (r *intakeRec) errMsg() string {
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(r.Body), &out)
	return out.Error.Message
}

func TestIntakeTooLongFieldSaysWhichOne(t *testing.T) {
	e := newTestEnv(t)
	tok := e.mkUser(true)
	long := strings.Repeat("长", 40) // varchar(32) 的列一定放不下
	for _, c := range []struct{ field, label string }{
		{"contact_phone", "联系电话"},
		{"package_type", "包装"},
		{"temperature_range", "温区"},
		{"pickup_contact_phone", "提货联系电话"},
	} {
		rec := e.intake(tok, map[string]string{c.field: long})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 填 40 个字返回 %d，应该是 400（这是用户填出来的，不是服务端故障）：%s",
				c.field, rec.Code, rec.Body)
			continue
		}
		if rec.errCode() != "FIELD_TOO_LONG" {
			t.Errorf("%s 的错误码是 %q，期望 FIELD_TOO_LONG", c.field, rec.errCode())
		}
		msg := rec.errMsg()
		if !strings.Contains(msg, c.label) {
			t.Errorf("%s 的提示是 %q —— 没点出是哪一栏（期望里面有「%s」）。\n"+
				"  客服看不懂列名，更看不懂 SQLSTATE。", c.field, msg, c.label)
		}
		if strings.Contains(msg, "SQLSTATE") || strings.Contains(msg, "character varying") {
			t.Errorf("%s 的提示里带着数据库原文：%q", c.field, msg)
		}
	}
}

// TestIntakeLongSourceIsClippedNotRejected 系统自己拼的来源标识不该把下单挡回去。
//
// source 是「组织名·姓名」，落进 varchar(32)。组织名长一点、
// 再配个邮箱式用户名就会超——那不是用户填错了什么，截断即可。
func TestIntakeLongSourceIsClippedNotRejected(t *testing.T) {
	e := newTestEnv(t)
	// mkUser 的用户名是 authz_test_ + 完整 uuid（47 字符），天然超长
	tok := e.mkUser(true)
	rec := e.intake(tok, nil)
	if rec.Code >= 400 {
		t.Fatalf("来源标识超长把下单挡回去了（%d）：%s", rec.Code, rec.Body)
	}
	no := rec.orderNo()
	if no == "" {
		t.Fatalf("返回体里没有单号：%s", rec.Body)
	}
	e.dropOrder(no)
	var src string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT source FROM ops_order WHERE order_no=$1`, no).Scan(&src); err != nil {
		t.Fatalf("回读来源失败：%v", err)
	}
	if n := len([]rune(src)); n > 32 || n == 0 {
		t.Errorf("落库的来源标识 %d 个字（%q）—— 应该被截到 32 以内且不为空", n, src)
	}
}

// dropOrder 注册一条按单号的清理，并且**删不掉就报出来**。
func (e *testEnv) dropOrder(no string) {
	e.t.Cleanup(func() {
		// t.Context() 在 Cleanup 之前就被取消了，这里必须用独立的 ctx，
		// 否则每一条清理都是 "context canceled"，而 `_, _ =` 会把它吞掉。
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM ops_order_event WHERE order_id IN (SELECT id FROM ops_order WHERE order_no=$1)`,
			`DELETE FROM ops_order_cargo_item WHERE order_id IN (SELECT id FROM ops_order WHERE order_no=$1)`,
			`DELETE FROM ops_order_stop WHERE order_id IN (SELECT id FROM ops_order WHERE order_no=$1)`,
			`DELETE FROM ops_order WHERE order_no=$1`,
		} {
			if _, err := e.pool.Exec(ctx, q, no); err != nil {
				e.t.Errorf("清理订单 %s 失败，垃圾留在库里了：%v", no, err)
			}
		}
	})
}
