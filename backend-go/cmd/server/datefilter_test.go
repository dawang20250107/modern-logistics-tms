package main

// 日期筛选按**北京日期**切，不是服务器时区。
//
// 这一条容易被无声改坏，所以单独钉住。
//
// 列是 timestamptz，而界面上填的是一个日期。两者比较有两种写法：
//
//   A  (col AT TIME ZONE 'Asia/Shanghai')::date >= $1::date     现在的写法
//   B  col >= $1::date                                          看起来等价
//
// B 会把日期按**服务器时区**转成时刻。生产上服务器多半跑 UTC
// （本机就是 Etc/UTC），于是 `'2026-08-30'::date` 变成 2026-08-30T00:00Z，
// 也就是北京时间当天早上 8 点——**北京 0 点到 8 点建的单全部落在"今天"之外**。
// 实测这个演示库：当天 1031 单里有 262 单在这个窗口内，占四分之一。
//
// A 的代价是列被函数包住、走不了普通索引；这是有意的取舍
// （筛选面本来就带分页，而"少四分之一的单"是没人能一眼看出来的错）。
// 谁要把它改成 B 换索引，这条用例会挡住。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestDateFilterUsesBeijingDay(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	token := e.mkUser(true)

	cst := time.FixedZone("CST", 8*3600)
	now := time.Now().In(cst)
	today := now.Format("2006-01-02")
	// 北京当天凌晨 2 点 —— UTC 下是**前一天** 18:00
	earlyBeijing := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, cst)

	no := "TZ" + fmt.Sprint(time.Now().UnixNano())
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO ops_order (id, created_at, updated_at, order_no, source, status, remark,
		  cargo_desc, cargo_quantity, cargo_volume_cbm, cargo_weight_ton, channel,
		  contact_name, contact_phone, destination, origin, parse_meta, raw_text,
		  business_type, cargo_value, delivery_address, delivery_contact_name, delivery_contact_phone,
		  is_deleted, is_hazardous, package_type, pickup_address, pickup_contact_name,
		  pickup_contact_phone, priority, quoted_amount, settlement_type, source_type,
		  temperature_range, sla_status, approval_remark, approval_status, ai_conversation_id,
		  cod_amount, cod_status, freight_payer, freight_term)
		VALUES (gen_random_uuid(), $2::timestamptz, now(), $1, 'tz-用例', 'draft', '',
		  '时区用例', 1, 0, 0, 'cs', '', '', '杭州', '上海', '{}'::jsonb, '',
		  'ftl', 0, '', '', '', false, false, '', '', '', '', 'normal', 0, 'monthly',
		  'enterprise', '', 'pending', '', 'none', '', 0, 'none', 'shipper', 'prepaid')`,
		no, earlyBeijing); err != nil {
		t.Fatalf("造单失败：%v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM ops_order WHERE order_no=$1`, no)
	})

	// 前提自检：这一单在 UTC 下确实落在前一天，否则这条用例什么都没测到
	var utcDay, cstDay string
	if err := e.pool.QueryRow(ctx, `
		SELECT (created_at)::date::text, (created_at AT TIME ZONE 'Asia/Shanghai')::date::text
		FROM ops_order WHERE order_no=$1`, no).Scan(&utcDay, &cstDay); err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if utcDay == cstDay {
		t.Skipf("这一单在 UTC 与北京时区下是同一天（%s）—— 换个时刻才测得到，跳过", utcDay)
	}
	if cstDay != today {
		t.Fatalf("造出来的单北京日期是 %s，期望 %s", cstDay, today)
	}

	filter := func(op, value string) int {
		q, _ := json.Marshal(map[string]any{
			"combinator": "and",
			"conditions": []map[string]any{
				{"field": "order_no", "op": "eq", "value": no},
				{"field": "created_at", "op": op, "value": value},
			},
		})
		rec := e.call(token, "GET",
			"/api/v1/orders?page_size=1&filter="+url.QueryEscape(string(q)), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("筛选返回 %d：%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Data struct {
				Total int `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析失败：%v", err)
		}
		return out.Data.Total
	}

	if n := filter("on", today); n != 1 {
		t.Errorf("按北京当天筛，这一单（北京 %s 02:00，UTC 还是 %s）没被筛出来（total=%d）。\n"+
			"  说明日期比较用的是服务器时区而不是北京时区——"+
			"北京 0 点到 8 点建的单会整段消失。", today, utcDay, n)
	}
	if n := filter("after", today); n != 1 {
		t.Errorf("「%s 之后」应包含当天这一单，实际 total=%d", today, n)
	}
	if n := filter("before", today); n != 1 {
		t.Errorf("「%s 之前」按日期比应包含当天（date <= date），实际 total=%d。\n"+
			"  如果这里变成 0，多半是把列和日期按时刻比了：那样「筛到 8/30」会把 8/30 整天排除掉。", today, n)
	}
	if n := filter("on", earlyBeijing.AddDate(0, 0, -1).Format("2006-01-02")); n != 0 {
		t.Errorf("按前一天筛不该筛出它，实际 total=%d（说明按 UTC 日期算了）", n)
	}
}
