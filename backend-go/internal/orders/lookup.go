package orders

// 全局查单 GET /api/v1/lookup?q= —— 对齐 apps/ops/lookup.global_search：
// answer 是「最相关单实体的实时上下文卡」，results 是跨实体可跳转列表。
// 解析优先级：运单号 → 车牌 → 电话 → 订单号 → 客户名，首个命中即返回答案卡。

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// lookupActive 「在途」口径，对齐 lookup._ACTIVE
var lookupActive = []string{"dispatched", "loaded", "departed", "in_transit", "arrived"}

func maskPhone(p string) string {
	r := []rune(p)
	if len(r) < 7 {
		return p
	}
	return string(r[:3]) + "****" + string(r[len(r)-4:])
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

type lookupWaybill struct {
	No, Origin, Destination, Status string
	Plate, DriverName, DriverPhone  string
	CustomerName                    string
	ETA                             *time.Time
}

// waybillCard 对齐 lookup._waybill_card：字段顺序即 fields 数组顺序，不可乱
func waybillCard(wb lookupWaybill) map[string]any {
	fields := []map[string]string{
		{"label": "线路", "value": orDash(wb.Origin) + "→" + orDash(wb.Destination)},
		{"label": "状态", "value": labelOr(wbstatus.Label, wb.Status)},
	}
	if wb.Plate != "" {
		fields = append(fields, map[string]string{"label": "车牌", "value": wb.Plate})
	}
	if wb.DriverName != "" {
		fields = append(fields, map[string]string{"label": "司机", "value": wb.DriverName + " " + maskPhone(wb.DriverPhone)})
	}
	if wb.CustomerName != "" {
		fields = append(fields, map[string]string{"label": "客户", "value": wb.CustomerName})
	}
	if wb.ETA != nil {
		fields = append(fields, map[string]string{"label": "ETA", "value": wb.ETA.In(lookupCST).Format("01-02 15:04")})
	}
	actions := []string{"view_waybill", "copy_reply"}
	if wb.DriverPhone != "" {
		actions = append(actions, "call_driver")
	}
	return map[string]any{
		"kind": "waybill", "title": "运单 " + wb.No, "waybill_no": wb.No,
		"driver_phone": wb.DriverPhone, "fields": fields, "actions": actions,
	}
}

var lookupCST = time.FixedZone("CST", 8*3600)

// statementStatusLabels 对账单状态中文名（Statement.STATUS_CHOICES）
var statementStatusLabels = map[string]string{
	"draft": "草稿", "confirmed": "已确认", "partial": "部分结算", "settled": "已结算",
}

const lookupWaybillSelect = `
SELECT w.waybill_no, w.origin, w.destination, w.status,
       COALESCE(v.plate_no,''), COALESCE(d.name,''), COALESCE(d.phone,''),
       COALESCE(c.name,''), COALESCE(w.estimated_arrival, w.planned_arrival)
FROM ops_waybill w
LEFT JOIN md_vehicle v ON v.id = w.vehicle_id
LEFT JOIN md_driver d ON d.id = w.driver_id
LEFT JOIN md_customer c ON c.id = w.customer_id`

func (h *Handler) scanLookupWaybill(ctx context.Context, where string, args ...any) *lookupWaybill {
	var wb lookupWaybill
	err := h.DB.QueryRow(ctx, lookupWaybillSelect+" "+where, args...).Scan(
		&wb.No, &wb.Origin, &wb.Destination, &wb.Status,
		&wb.Plate, &wb.DriverName, &wb.DriverPhone, &wb.CustomerName, &wb.ETA)
	if err != nil {
		return nil
	}
	return &wb
}

// globalLookup 答案卡：五级解析，命中即返回
func (h *Handler) globalLookup(ctx context.Context, q string) map[string]any {
	none := map[string]any{"kind": "none"}
	if len([]rune(q)) < 2 {
		return none
	}
	like := "%" + q + "%"

	// 1) 运单号：先精确（iexact）后模糊
	if wb := h.scanLookupWaybill(ctx, "WHERE lower(w.waybill_no)=lower($1) LIMIT 1", q); wb != nil {
		return waybillCard(*wb)
	}
	if wb := h.scanLookupWaybill(ctx, "WHERE w.waybill_no ILIKE $1 LIMIT 1", like); wb != nil {
		return waybillCard(*wb)
	}

	// 2) 车牌 → 该车当前运单
	var vehID, plate string
	if h.DB.QueryRow(ctx, `SELECT id::text, plate_no FROM md_vehicle
		WHERE plate_no ILIKE $1 AND NOT is_deleted LIMIT 1`, like).Scan(&vehID, &plate) == nil {
		if wb := h.scanLookupWaybill(ctx,
			`WHERE w.vehicle_id=$1::uuid AND w.status <> 'voided' ORDER BY w.created_at DESC, w.id LIMIT 1`, vehID); wb != nil {
			card := waybillCard(*wb)
			card["title"] = "车辆 " + plate + " · 当前运单"
			return card
		}
		return map[string]any{"kind": "vehicle", "title": "车辆 " + plate,
			"fields":  []map[string]string{{"label": "状态", "value": "当前无在途运单"}},
			"actions": []string{}}
	}

	// 3) 电话（纯数字且 ≥7 位）→ 司机当前运单
	if isDigits(q) && len(q) >= 7 {
		var drvID, drvName, drvPhone string
		if h.DB.QueryRow(ctx, `SELECT id::text, name, phone FROM md_driver
			WHERE phone=$1 AND NOT is_deleted LIMIT 1`, q).Scan(&drvID, &drvName, &drvPhone) == nil {
			if wb := h.scanLookupWaybill(ctx,
				`WHERE w.driver_id=$1::uuid AND w.status <> 'voided' ORDER BY w.created_at DESC, w.id LIMIT 1`, drvID); wb != nil {
				card := waybillCard(*wb)
				card["title"] = "司机 " + drvName + " · 当前运单"
				return card
			}
			return map[string]any{"kind": "driver", "title": "司机 " + drvName,
				"fields": []map[string]string{
					{"label": "电话", "value": maskPhone(drvPhone)},
					{"label": "状态", "value": "当前无在途运单"},
				},
				"actions": []string{}}
		}
	}

	// 4) 订单号
	for _, cond := range []struct{ where, arg string }{
		{"WHERE NOT o.is_deleted AND lower(o.order_no)=lower($1) LIMIT 1", q},
		{"WHERE NOT o.is_deleted AND o.order_no ILIKE $1 LIMIT 1", like},
	} {
		var no, origin, dest, status, cust string
		err := h.DB.QueryRow(ctx, `
			SELECT o.order_no, o.origin, o.destination, o.status, COALESCE(c.name,'散客')
			FROM ops_order o LEFT JOIN md_customer c ON c.id = o.customer_id `+cond.where, cond.arg).
			Scan(&no, &origin, &dest, &status, &cust)
		if err == nil {
			return map[string]any{
				"kind": "order", "title": "订单 " + no, "order_no": no,
				"fields": []map[string]string{
					{"label": "线路", "value": orDash(origin) + "→" + orDash(dest)},
					{"label": "状态", "value": labelOr(orderStatusLabel, status)},
					{"label": "客户", "value": cust},
				},
				"actions": []string{"view_order"},
			}
		}
	}

	// 5) 客户名 / 编码 → 概览
	var custID, custName string
	var creditDays int
	if h.DB.QueryRow(ctx, `SELECT id::text, name, credit_days FROM md_customer
		WHERE NOT is_deleted AND (name ILIKE $1 OR lower(code)=lower($2)) LIMIT 1`, like, q).
		Scan(&custID, &custName, &creditDays) == nil {
		var active int
		_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_waybill
			WHERE customer_id=$1::uuid AND status = ANY($2)`, custID, lookupActive).Scan(&active)
		return map[string]any{
			"kind": "customer", "title": "客户 " + custName, "customer_id": custID,
			"fields": []map[string]string{
				{"label": "在途运单", "value": itoa(active)},
				{"label": "账期", "value": itoa(creditDays) + " 天"},
			},
			"actions": []string{},
		}
	}
	return none
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// Lookup GET /api/v1/lookup?q=
func (h *Handler) Lookup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"answer": map[string]any{"kind": "none"}, "results": []any{}})
		return
	}
	like := "%" + q + "%"
	answer := h.globalLookup(ctx, q)
	results := []map[string]any{}

	// 运单（有详情页）
	rows, err := h.DB.Query(ctx, `
		SELECT w.waybill_no, w.origin, w.destination, w.status, COALESCE(c.name,'')
		FROM ops_waybill w LEFT JOIN md_customer c ON c.id = w.customer_id
		WHERE w.status <> 'voided'
		  AND (w.waybill_no ILIKE $1 OR c.name ILIKE $1 OR w.origin ILIKE $1 OR w.destination ILIKE $1)
		ORDER BY w.created_at DESC, w.id LIMIT 5`, like)
	if err == nil {
		for rows.Next() {
			var no, origin, dest, status, cust string
			if rows.Scan(&no, &origin, &dest, &status, &cust) != nil {
				break
			}
			sub := orDash(origin) + "→" + orDash(dest) + " · " + labelOr(wbstatus.Label, status)
			if cust != "" {
				sub += " · " + cust
			}
			results = append(results, map[string]any{
				"kind": "waybill", "title": "运单 " + no, "subtitle": sub, "path": "/waybills/" + no})
		}
		rows.Close()
	}

	// 订单（有详情页，path 用 id）
	rows, err = h.DB.Query(ctx, `
		SELECT o.id::text, o.order_no, o.origin, o.destination, o.status, COALESCE(c.name,'')
		FROM ops_order o LEFT JOIN md_customer c ON c.id = o.customer_id
		WHERE NOT o.is_deleted
		  AND (o.order_no ILIKE $1 OR c.name ILIKE $1 OR o.origin ILIKE $1 OR o.destination ILIKE $1)
		ORDER BY o.created_at DESC, o.id LIMIT 5`, like)
	if err == nil {
		for rows.Next() {
			var id, no, origin, dest, status, cust string
			if rows.Scan(&id, &no, &origin, &dest, &status, &cust) != nil {
				break
			}
			sub := orDash(origin) + "→" + orDash(dest) + " · " + labelOr(orderStatusLabel, status)
			if cust != "" {
				sub += " · " + cust
			}
			results = append(results, map[string]any{
				"kind": "order", "title": "订单 " + no, "subtitle": sub, "path": "/orders/" + id})
		}
		rows.Close()
	}

	// 客户 / 承运商 / 对账单（跳对应台账页）
	rows, err = h.DB.Query(ctx, `
		SELECT c.id::text, c.name, c.credit_days,
		       (SELECT count(*) FROM ops_waybill w WHERE w.customer_id=c.id AND w.status = ANY($2)) AS active
		FROM md_customer c WHERE NOT c.is_deleted AND (c.name ILIKE $1 OR c.code ILIKE $1)
		ORDER BY c.code, c.id LIMIT 3`, like, lookupActive)
	if err == nil {
		for rows.Next() {
			var id, name string
			var days, active int
			if rows.Scan(&id, &name, &days, &active) != nil {
				break
			}
			results = append(results, map[string]any{
				"kind": "customer", "title": "客户 " + name,
				"subtitle": "在途 " + itoa(active) + " · 账期 " + itoa(days) + " 天", "path": "/fleet"})
		}
		rows.Close()
	}
	rows, err = h.DB.Query(ctx, `
		SELECT name, COALESCE(city,''), COALESCE(grade,'') FROM md_carrier
		WHERE NOT is_deleted AND (name ILIKE $1 OR code ILIKE $1)
		ORDER BY code, id LIMIT 3`, like)
	if err == nil {
		for rows.Next() {
			var name, city, grade string
			if rows.Scan(&name, &city, &grade) != nil {
				break
			}
			results = append(results, map[string]any{
				"kind": "carrier", "title": "承运商 " + name,
				"subtitle": dashIfEmpty(city) + " · " + dashIfEmpty(grade) + "级", "path": "/fleet"})
		}
		rows.Close()
	}
	rows, err = h.DB.Query(ctx, `
		SELECT statement_no, COALESCE(counterparty_name,''), status FROM fin_statement
		WHERE statement_no ILIKE $1 OR counterparty_name ILIKE $1
		ORDER BY created_at DESC, id LIMIT 3`, like)
	if err == nil {
		for rows.Next() {
			var no, party, status string
			if rows.Scan(&no, &party, &status) != nil {
				break
			}
			results = append(results, map[string]any{
				"kind": "statement", "title": "对账单 " + no,
				"subtitle": party + " · " + labelOr(statementStatusLabels, status), "path": "/reconciliation"})
		}
		rows.Close()
	}

	if len(results) > 12 {
		results = results[:12]
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"answer": answer, "results": results})
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func env(k string) string { return os.Getenv(k) }

// IntegrationStatus GET /api/v1/integrations/status —— 对齐 integrations.status.integration_status
func (h *Handler) IntegrationStatus(w http.ResponseWriter, r *http.Request) {
	state := func(ok bool, live, other string) string {
		if ok {
			return live
		}
		return other
	}
	ymmOK := env("YMM_APP_KEY") != "" && env("YMM_APP_SECRET") != "" && env("YMM_ACCESS_TOKEN") != ""
	feishuOK := env("FEISHU_APP_ID") != "" && env("FEISHU_APP_SECRET") != ""
	wechatOK := env("WECHAT_PROVIDER") != "" && (env("WECHAT_CORP_ID") != "" || env("WECHAT_PROVIDER") == "personal")
	httpx.JSON(w, http.StatusOK, map[string]any{
		"integrations": []map[string]string{
			{"key": "ymm", "name": "运满满调车比价",
				"state": state(ymmOK, "live", "fallback"),
				"note":  "已实现；未配置凭证时返回离线参考价。"},
			{"key": "feishu", "name": "飞书 Bot 卡片 / 多维表格",
				"state": state(feishuOK, "live", "reserved"),
				"note":  "卡片构造可用；推送与多维表同步预留，配置 FEISHU_APP_ID/SECRET 后启用。"},
			{"key": "wechat", "name": "微信接入",
				"state": state(wechatOK, "live", "reserved"),
				"note":  "企业微信/个人微信自动化预留，配置 WECHAT_* 后启用。"},
		},
	})
}
