package main

// CSV 导出：数据量上去之后还说不说实话。
//
// 三个导出端点都写着 LIMIT 5000，而且截断之后不留任何痕迹——
// 5 万单的库里点「导出全部」，拿到的是最近 5000 条，文件看着完整、
// 表头齐、行也齐，只是少了 90%。拿它去对账或报税，差额没人对得出来。
//
// 这类问题这套系统已经栽过一次（调度台把 8336 说成 20），是同一条：
// 安静地把一部分当成全部。

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// TestOrderExportSaysWhenTruncated 导出被截断时必须在文件里说明。
//
// 只加响应头不够：用户拿到的是一个 .csv 文件，头早就丢了。
// 唯一到得了人眼前的地方，是文件本身的最后一行。
func TestOrderExportSaysWhenTruncated(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)

	var total int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ops_order WHERE NOT is_deleted`).Scan(&total); err != nil {
		t.Fatalf("数订单失败：%v", err)
	}
	if total <= httpx.ExportMaxRows {
		t.Skipf("库里只有 %d 单，不到导出上限 %d，这条用例需要更大的数据集", total, httpx.ExportMaxRows)
	}

	rec := e.call(token, "GET", "/api/v1/orders/export", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("导出返回 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "导出已截断") {
		lines := strings.Count(body, "\n")
		t.Errorf("库里有 %d 单、导出只给了约 %d 行，文件里却没有任何截断说明——"+
			"用户会把它当成全部", total, lines-1)
	}
	if rec.Header().Get("X-Export-Truncated") != "1" {
		t.Error("截断时没有 X-Export-Truncated 头（给程序化调用方看的）")
	}
}

// TestOrderExportHonoursFilters 导出必须跟着筛选走。
//
// 界面上「导出」按钮就在搜索框和筛选器旁边，用户的预期是导出他正看着的那批。
// 端点本来就支持 search=/filter=，是前端没把它们带上——
// 于是筛出 12 单、点导出、拿到 5000 条毫不相干的。
func TestOrderExportHonoursFilters(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	tag := "EXP" + uuid.NewString()[:8]
	for i := 0; i < 3; i++ {
		e.mkOrderWithRemark(tag)
	}

	rec := e.call(token, "GET", "/api/v1/orders/export?search="+tag, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("导出返回 %d", rec.Code)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(rec.Body.Bytes(),
		[]byte{0xEF, 0xBB, 0xBF}))).ReadAll()
	if err != nil {
		t.Fatalf("解析 CSV 失败：%v", err)
	}
	if got := len(rows) - 1; got != 3 {
		t.Errorf("按 %s 搜索应导出 3 行，实际 %d 行 —— 导出没跟着筛选走", tag, got)
	}
}

// mkOrderWithRemark 造一张备注可检索的订单（remark 在 searchCols 里）。
func (e *testEnv) mkOrderWithRemark(remark string) {
	e.t.Helper()
	id := uuid.NewString()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO ops_order (id, created_at, updated_at, order_no, source, status, remark,
		  cargo_desc, cargo_quantity, cargo_volume_cbm, cargo_weight_ton, channel,
		  contact_name, contact_phone, destination, origin, parse_meta, raw_text,
		  business_type, cargo_value, delivery_address, delivery_contact_name,
		  delivery_contact_phone, is_deleted, is_hazardous, package_type, pickup_address,
		  pickup_contact_name, pickup_contact_phone, priority, quoted_amount, settlement_type,
		  source_type, temperature_range, sla_status, approval_remark, approval_status,
		  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term)
		VALUES ($1::uuid, now(), now(), $2, 'cs', 'pooled', $3, '测试货', 1, 1, 1, 'cs',
		  '张三', '13800000000', '上海', '杭州', '{}'::jsonb, '', 'ftl', 0, '收货地址', '李四', '13900000000',
		  false, false, '纸箱', '发货地址', '王五', '13700000000', 'normal', 0, 'monthly',
		  'enterprise', '', 'pending', '', 'none', '', 0, 'none', 'shipper', 'prepaid')`,
		id, "EXP"+uuid.NewString()[:10], remark); err != nil {
		e.t.Fatalf("造订单失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM ops_order WHERE id=$1::uuid`, id)
	})
}
