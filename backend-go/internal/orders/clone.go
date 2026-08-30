package orders

// 复制建单：POST /orders/{id}/clone —— 对齐 apps/ops/intake.clone_order。
// 以蓝本订单的表头字段 + 货物明细 + 站点生成新草稿；渠道/来源/客户沿用蓝本
// （不取操作人组织·姓名），建单人记为当前操作人。

import (
	"context"
	"net/http"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// Clone POST /api/v1/orders/{id}/clone
func (h *Handler) Clone(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.manage") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}

	// 表头字段（_ORDER_FIELDS 全集，含空值——clone 直接整体复制，不做“非空才带”过滤）
	sel := make([]string, 0, len(orderFields))
	for _, f := range orderFields {
		switch f {
		case "cargo_weight_ton", "cargo_volume_cbm", "cargo_value", "quoted_amount", "cod_amount":
			sel = append(sel, f+"::text AS "+f) // Decimal 保刻度
		default:
			sel = append(sel, f)
		}
	}
	rows, err := h.DB.Query(ctx,
		"SELECT channel, source, customer_id::text, "+joinComma(sel)+" FROM ops_order WHERE id=$1::uuid", id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取蓝本失败")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	fds := rows.FieldDescriptions()
	vals, err := rows.Values()
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取蓝本失败")
		return
	}
	data := map[string]any{}
	var channel, source string
	var customerID *string
	for i, fd := range fds {
		switch fd.Name {
		case "channel":
			channel, _ = vals[i].(string)
		case "source":
			source, _ = vals[i].(string)
		case "customer_id":
			if s, ok := vals[i].(string); ok && s != "" {
				customerID = &s
			}
		default:
			if vals[i] != nil {
				data[fd.Name] = vals[i]
			}
		}
	}
	rows.Close()

	cargoItems, err := h.childRows(ctx, `
		SELECT name, quantity, weight_ton::text AS weight_ton, volume_cbm::text AS volume_cbm,
		       package_type, temperature_range, remark
		FROM ops_order_cargo_item WHERE order_id=$1::uuid ORDER BY seq, id`, id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取货物明细失败")
		return
	}
	stops, err := h.childRows(ctx, `
		SELECT stop_type, city, address, contact_name, contact_phone,
		       expected_start, expected_end, cargo_note
		FROM ops_order_stop WHERE order_id=$1::uuid ORDER BY seq, id`, id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取站点失败")
		return
	}

	orderID, code, msg := h.createOrder(ctx, createParams{
		Data: data, Channel: channel, Source: source, Status: "draft",
		CustomerID: customerID, CargoItems: cargoItems, Stops: stops, ActorID: me.ID,
	})
	if code != "" {
		httpx.Err(w, http.StatusInternalServerError, code, msg)
		return
	}
	h.respondOne(w, r, orderID, me)
}

// childRows 泛化读取子表为 map 列表（列别名即键）
func (h *Handler) childRows(ctx context.Context, sql, id string) ([]map[string]any, error) {
	rows, err := h.DB.Query(ctx, sql, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fds := rows.FieldDescriptions()
	out := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(fds))
		for i, fd := range fds {
			if vals[i] != nil {
				m[fd.Name] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
