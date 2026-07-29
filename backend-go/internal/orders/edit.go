package orders

// 编辑订单：POST /orders/{id}/edit —— 对齐 apps/ops/intake.update_order。
// 草稿/待确认/已确认可改；支持整体替换货物明细与站点并重算货量；
// 落字段级变更快照（改了哪个字段、从什么变成什么）进 updated 事件 payload，
// 满足审计与版本追溯；改后重跑审批闸。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var fieldLabels = map[string]string{
	"contact_name": "联系人", "contact_phone": "联系电话", "origin": "始发地", "destination": "目的地",
	"pickup_address": "提货地址", "delivery_address": "送货地址", "cargo_desc": "货物",
	"cargo_quantity": "件数", "cargo_weight_ton": "重量(吨)", "cargo_volume_cbm": "体积(方)",
	"cargo_value": "货值", "package_type": "包装", "is_hazardous": "危险品", "temperature_range": "温区",
	"quoted_amount": "报价", "expected_pickup_at": "期望提货", "expected_delivery_at": "期望送达",
	"priority": "优先级", "business_type": "业务类型", "settlement_type": "结算方式", "remark": "备注",
}

// Edit POST /api/v1/orders/{id}/edit {fields, cargo_items, stops}
func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Fields     map[string]any   `json:"fields"`
		CargoItems []map[string]any `json:"cargo_items"`
		Stops      []map[string]any `json:"stops"`
	}
	raw := map[string]json.RawMessage{}
	buf, _ := readAll(r)
	_ = json.Unmarshal(buf, &raw)
	_ = json.Unmarshal(buf, &body)
	// 区分「未传」与「传了空数组」：仅在显式传入时替换子表（对齐 Python 的 is not None）
	_, hasCargo := raw["cargo_items"]
	_, hasStops := raw["stops"]

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var customerID *string
	if err := tx.QueryRow(ctx, `SELECT status, customer_id::text FROM ops_order WHERE id=$1::uuid FOR UPDATE`, id).
		Scan(&status, &customerID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	if status == "converted" || status == "completed" || status == "cancelled" {
		httpx.Err(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "已派单/完成/取消的订单不可编辑。")
		return
	}
	// 原本无客户且本次传了 customer → 补挂
	if customerID == nil {
		if c, okc := body.Fields["customer"].(string); okc && c != "" {
			if _, err := uuid.Parse(c); err == nil {
				if _, err := tx.Exec(ctx, `UPDATE ops_order SET customer_id=$2::uuid WHERE id=$1::uuid`, id, c); err != nil {
					slog.Error("补挂客户失败", "order_id", id, "customer_id", c, "err", err)
				}
			}
		}
	}

	// 白名单收敛 + 时间解析 + 补全（与建单同一套 enrich）
	clean := map[string]any{}
	for _, k := range orderFields {
		v, has := body.Fields[k]
		if !has {
			continue
		}
		if k == "expected_pickup_at" || k == "expected_delivery_at" {
			if s, isStr := v.(string); isStr {
				if dt := coerceDT(s); dt != nil {
					v = dt
				} else {
					continue
				}
			}
		}
		clean[k] = v
	}
	enrich(clean)

	// 落库前快照：读当前值做字段级 diff
	changes, err := diffOrderFields(ctx, tx, id, clean)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取原值失败")
		return
	}
	// 空值不覆盖（对齐 setattr(order, k, v if v not in (None,"") else 原值)）
	sets, args := []string{}, []any{id}
	for k, v := range clean {
		if v == nil || v == "" {
			continue
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", k, len(args)))
	}
	if len(sets) > 0 {
		if _, err := tx.Exec(ctx,
			"UPDATE ops_order SET "+strings.Join(sets, ", ")+", updated_at=now() WHERE id=$1::uuid", args...); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "更新失败："+err.Error())
			return
		}
	}

	extra := []string{}
	if hasCargo {
		if _, err := tx.Exec(ctx, `DELETE FROM ops_order_cargo_item WHERE order_id=$1::uuid`, id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "货物明细清理失败")
			return
		}
		seq := 0
		for _, item := range body.CargoItems {
			if strings.TrimSpace(str(item, "name")) == "" {
				continue
			}
			seq++
			row := map[string]any{"quantity": 0, "weight_ton": "0", "volume_cbm": "0", "package_type": "", "temperature_range": "", "remark": ""}
			for _, k := range cargoItemFields {
				if v, okv := item[k]; okv && v != nil && v != "" {
					row[k] = v
				}
			}
			cid, _ := uuid.NewV7()
			if _, err := tx.Exec(ctx, `
				INSERT INTO ops_order_cargo_item (id, created_at, updated_at, order_id, seq, name, quantity, weight_ton, volume_cbm, package_type, temperature_range, remark)
				VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				cid.String(), id, seq, row["name"], row["quantity"], row["weight_ton"], row["volume_cbm"],
				row["package_type"], row["temperature_range"], row["remark"]); err != nil {
				httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "货物明细写入失败")
				return
			}
		}
		// recompute_cargo_totals：无明细时归零（对齐 Django 的 aggregate 结果为 None→0）
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order o SET cargo_quantity = s.q, cargo_weight_ton = s.w, cargo_volume_cbm = s.v, updated_at = now()
			FROM (SELECT COALESCE(sum(quantity),0) q, COALESCE(sum(weight_ton),0) w, COALESCE(sum(volume_cbm),0) v
			      FROM ops_order_cargo_item WHERE order_id = $1::uuid) s WHERE o.id = $1::uuid`, id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "货量汇总失败")
			return
		}
		extra = append(extra, "货物明细")
	}
	if hasStops {
		if _, err := tx.Exec(ctx, `DELETE FROM ops_order_stop WHERE order_id=$1::uuid`, id); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "站点清理失败")
			return
		}
		seq := 0
		for _, sp := range body.Stops {
			if str(sp, "address") == "" && str(sp, "city") == "" {
				continue
			}
			seq++
			row := map[string]any{"stop_type": "pickup", "city": "", "address": "", "contact_name": "", "contact_phone": "", "cargo_note": ""}
			var es, ee any
			for _, k := range stopFields {
				if v, okv := sp[k]; okv && v != nil && v != "" {
					if k == "expected_start" || k == "expected_end" {
						if s, isStr := v.(string); isStr {
							v = coerceDT(s)
						}
						if k == "expected_start" {
							es = v
						} else {
							ee = v
						}
						continue
					}
					row[k] = v
				}
			}
			sid, _ := uuid.NewV7()
			if _, err := tx.Exec(ctx, `
				INSERT INTO ops_order_stop (id, created_at, updated_at, order_id, seq, stop_type, city, address, contact_name, contact_phone, expected_start, expected_end, cargo_note)
				VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				sid.String(), id, seq, row["stop_type"], row["city"], row["address"],
				row["contact_name"], row["contact_phone"], es, ee, row["cargo_note"]); err != nil {
				httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "站点写入失败")
				return
			}
		}
		extra = append(extra, "站点")
	}

	// updated 事件（含字段级变更快照）
	if changes == nil {
		changes = []map[string]any{}
	}
	if extra == nil {
		extra = []string{}
	}
	_ = txEvent(ctx, tx, id, "updated", "", status, me.ID, "cs",
		map[string]any{"changes": changes, "changed_collections": extra})

	// 审批闸重算（报价≥5万 或 货值≥50万 → 待审批）
	var quoted, cargoValue float64
	var approval string
	_ = tx.QueryRow(ctx, `SELECT quoted_amount::float8, cargo_value::float8, approval_status
		FROM ops_order WHERE id=$1::uuid`, id).Scan(&quoted, &cargoValue, &approval)
	if (quoted >= 50000 || cargoValue >= 500000) && approval == "none" {
		if _, err := tx.Exec(ctx, `UPDATE ops_order SET approval_status='pending', updated_at=now() WHERE id=$1::uuid`, id); err == nil {
			_ = txEvent(ctx, tx, id, "approval_required", "", "", me.ID, "system", map[string]any{
				"quoted_amount": fmt.Sprintf("%.2f", quoted), "cargo_value": fmt.Sprintf("%.2f", cargoValue),
			})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	h.respondOneStatus(w, r, id, me, http.StatusOK)
}

// diffOrderFields 读当前值与待写值比对，产出字段级变更快照
func diffOrderFields(ctx context.Context, tx pgx.Tx, id string, clean map[string]any) ([]map[string]any, error) {
	if len(clean) == 0 {
		return nil, nil
	}
	cols := make([]string, 0, len(clean))
	for k := range clean {
		// Decimal / 时间统一取文本，避免驱动类型差异干扰比对
		switch k {
		case "cargo_weight_ton", "cargo_volume_cbm", "cargo_value", "quoted_amount", "cod_amount":
			cols = append(cols, k+"::text AS "+k)
		default:
			cols = append(cols, k)
		}
	}
	rows, err := tx.Query(ctx, "SELECT "+strings.Join(cols, ", ")+" FROM ops_order WHERE id=$1::uuid", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	fds := rows.FieldDescriptions()
	vals, err := rows.Values()
	if err != nil {
		return nil, err
	}
	old := map[string]any{}
	for i, fd := range fds {
		old[fd.Name] = vals[i]
	}
	changes := []map[string]any{}
	for _, k := range orderFields { // 按字段声明序输出，结果稳定
		v, has := clean[k]
		if !has {
			continue
		}
		newV := v
		if newV == nil || newV == "" {
			newV = old[k]
		}
		if !sameValue(old[k], newV) {
			changes = append(changes, map[string]any{
				"field": k, "label": labelOf(k),
				"from": jsonableOld(k, old[k]), "to": jsonableNew(newV),
			})
		}
	}
	return changes, nil
}

func labelOf(k string) string {
	if v, ok := fieldLabels[k]; ok {
		return v
	}
	return k
}

// sameValue 跨类型的宽松相等（DB 返回 string/数值，入参可能是 json 数值）
func sameValue(a, b any) bool {
	return fmt.Sprint(jsonable(a)) == fmt.Sprint(jsonable(b))
}

// jsonableOld 原值来自库（Decimal/datetime），按 Django _jsonable 转 float / isoformat
func jsonableOld(field string, v any) any {
	if v == nil {
		return nil
	}
	switch field {
	case "cargo_weight_ton", "cargo_volume_cbm", "cargo_value", "quoted_amount", "cod_amount":
		if s, ok := v.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		}
	}
	if t, ok := v.(time.Time); ok {
		return pyISOTime(t)
	}
	return v
}

// jsonableNew 新值是请求原样传入的（字符串就保持字符串），仅 datetime 需 isoformat
func jsonableNew(v any) any {
	if t, ok := v.(time.Time); ok {
		return pyISOTime(t)
	}
	return v
}

// pyISOTime 复刻 Python datetime.isoformat()（数字时区偏移，微秒零则省略）
func pyISOTime(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00")
}

func jsonable(v any) any { return v }

func readAll(r *http.Request) ([]byte, error) {
	const maxBody = 4 << 20
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for len(buf) < maxBody {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}
