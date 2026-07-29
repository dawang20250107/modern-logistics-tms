package orders

// 建单核心路径（对齐 apps/ops/intake.create_order_from_intake）：取号 → 全默认值
// INSERT → 货物明细 + 货量汇总 → 站点 → 建单事件 → 审批闸，全程单事务。
// intake / clone 共用此函数：前者来源取操作人组织·姓名，后者沿用蓝本订单的渠道与来源。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type createParams struct {
	RawText    string           // 原文（仅 intake 有）
	Data       map[string]any   // 已解析/补全的字段面
	Channel    string           // cs / self / miniprogram / wechat_group / api
	Source     string           // 来源标识
	Status     string           // draft / pending_confirm ...
	CustomerID *string          // 已对齐的客户
	CargoItems []map[string]any // 货物明细
	Stops      []map[string]any // 站点
	ActorID    string           // 建单人
	ParseMeta  map[string]any   // 解析元信息（{"source":"rule"}；fields._meta 可覆盖）
}

// createOrder 返回新订单主键；失败返回业务错误码与文案（调用方直接回写响应）
func (h *Handler) createOrder(ctx context.Context, p createParams) (string, string, string) {
	// 白名单 + 时间字段解析（非法值丢弃，对齐 _coerce_datetimes）
	cols, vals := []string{}, []any{}
	for _, k := range orderFields {
		v, ok := p.Data[k]
		if !ok || v == nil || v == "" {
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
		cols = append(cols, k)
		vals = append(vals, v)
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		return "", "INTERNAL", "事务开启失败"
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orderNo, err := nextNo(ctx, tx, "DD")
	if err != nil {
		return "", "INTERNAL", "取号失败"
	}
	id, _ := uuid.NewV7()
	orderID := id.String()

	base := map[string]any{
		"id": orderID, "created_at": httpx.Micros(time.Now()), "updated_at": httpx.Micros(time.Now()), "is_deleted": false,
		"order_no": orderNo, "channel": p.Channel, "source": p.Source, "status": p.Status,
		"source_type": "enterprise", "business_type": "ftl", "priority": "normal",
		"settlement_type": "monthly", "freight_term": "prepaid", "freight_payer": "shipper",
		"cod_amount": "0", "cod_status": "none",
		"contact_name": "", "contact_phone": "", "origin": "", "destination": "",
		"pickup_address": "", "pickup_contact_name": "", "pickup_contact_phone": "",
		"delivery_address": "", "delivery_contact_name": "", "delivery_contact_phone": "",
		"cargo_desc": "", "cargo_quantity": 0, "cargo_weight_ton": "0", "cargo_volume_cbm": "0",
		"cargo_value": "0", "package_type": "", "is_hazardous": false, "temperature_range": "",
		"quoted_amount": "0", "sla_status": "pending", "approval_status": "none", "approval_remark": "",
		"raw_text": p.RawText, "ai_conversation_id": aiConvID(p), "parse_meta": metaJSON(p.ParseMeta), "remark": "",
		"customer_id": p.CustomerID, "created_by_id": p.ActorID,
	}
	for i, c := range cols {
		base[c] = vals[i]
	}
	insCols, insVals, phs := []string{}, []any{}, []string{}
	for c, v := range base {
		insCols = append(insCols, c)
		insVals = append(insVals, v)
		phs = append(phs, fmt.Sprintf("$%d", len(insVals)))
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO ops_order ("+strings.Join(insCols, ",")+") VALUES ("+strings.Join(phs, ",")+")", insVals...); err != nil {
		return "", "INTERNAL", "建单失败：" + err.Error()
	}

	// 货物明细 + 货量汇总回写
	seq := 0
	for _, item := range p.CargoItems {
		if strings.TrimSpace(str(item, "name")) == "" {
			continue
		}
		seq++
		row := map[string]any{"quantity": 0, "weight_ton": "0", "volume_cbm": "0", "package_type": "", "temperature_range": "", "remark": ""}
		for _, k := range cargoItemFields {
			if v, ok := item[k]; ok && v != nil && v != "" {
				row[k] = v
			}
		}
		cid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_order_cargo_item (id, created_at, updated_at, order_id, seq, name, quantity, weight_ton, volume_cbm, package_type, temperature_range, remark)
			VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			cid.String(), orderID, seq, row["name"], row["quantity"], row["weight_ton"], row["volume_cbm"],
			row["package_type"], row["temperature_range"], row["remark"]); err != nil {
			return "", "INTERNAL", "货物明细写入失败"
		}
	}
	if seq > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE ops_order o SET cargo_quantity = s.q, cargo_weight_ton = s.w, cargo_volume_cbm = s.v, updated_at = now()
			FROM (SELECT COALESCE(sum(quantity),0) q, COALESCE(sum(weight_ton),0) w, COALESCE(sum(volume_cbm),0) v
			      FROM ops_order_cargo_item WHERE order_id = $1) s WHERE o.id = $1`, orderID); err != nil {
			return "", "INTERNAL", "货量汇总失败"
		}
	}

	// 站点
	seq = 0
	for _, sp := range p.Stops {
		if str(sp, "address") == "" && str(sp, "city") == "" {
			continue
		}
		seq++
		row := map[string]any{"stop_type": "pickup", "city": "", "address": "", "contact_name": "", "contact_phone": "", "cargo_note": ""}
		var es, ee any
		for _, k := range stopFields {
			if v, ok := sp[k]; ok && v != nil && v != "" {
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
			sid.String(), orderID, seq, row["stop_type"], row["city"], row["address"],
			row["contact_name"], row["contact_phone"], es, ee, row["cargo_note"]); err != nil {
			return "", "INTERNAL", "站点写入失败"
		}
	}

	// 建单事件 + 审批闸（报价≥5万 或 货值≥50万 → 待审批）
	evt := func(eventType, toStatus string, payload map[string]any) error {
		pj, _ := json.Marshal(payload)
		eid, _ := uuid.NewV7()
		src := "system"
		if eventType == "created" {
			src = "cs"
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO ops_order_event (id, created_at, updated_at, event_time, order_id, event_type, from_status, to_status, actor_id, source, payload)
			VALUES ($1, now(), now(), clock_timestamp(), $2, $3, '', $4, $5, $6, $7)`,
			eid.String(), orderID, eventType, toStatus, p.ActorID, src, pj)
		return err
	}
	if err := evt("created", p.Status, map[string]any{"channel": p.Channel}); err != nil {
		return "", "INTERNAL", "事件写入失败"
	}
	var quoted, cargoValue float64
	_ = tx.QueryRow(ctx, "SELECT quoted_amount::float8, cargo_value::float8 FROM ops_order WHERE id=$1", orderID).Scan(&quoted, &cargoValue)
	if quoted >= 50000 || cargoValue >= 500000 {
		if _, err := tx.Exec(ctx, "UPDATE ops_order SET approval_status='pending', updated_at=now() WHERE id=$1", orderID); err == nil {
			_ = evt("approval_required", "", map[string]any{
				"quoted_amount": strconv.FormatFloat(quoted, 'f', 2, 64),
				"cargo_value":   strconv.FormatFloat(cargoValue, 'f', 2, 64),
			})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "INTERNAL", "提交失败"
	}
	return orderID, "", ""
}

// metaJSON 解析元信息落库（Django 侧 parse_meta 为 JSONField，规则解析写 {"source":"rule"}）
func metaJSON(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// aiConvID 对齐 Django：显式字段优先，其次取 parse_meta.conversation_id
func aiConvID(p createParams) string {
	if v := str(p.Data, "ai_conversation_id"); v != "" {
		return v
	}
	if c, ok := p.ParseMeta["conversation_id"].(string); ok {
		return c
	}
	return ""
}
