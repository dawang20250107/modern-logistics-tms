package orders

// 订单侧的外部比价与转运单：
//   GET  /orders/{id}/ymm-quote   运满满调车运费比价（未接入则离线参考价 + 历史混合）
//   POST /orders/{id}/convert     订单转运单（不带承运商/车辆的最简转换）
//
// 对齐 apps/integrations/ymm.freight_quote 与 apps/ops/intake.convert_order_to_waybill。

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"
)

// offlineEstimate 离线参考价：基础价 + 抛重折算后的吨位加权
func offlineEstimate(origin, destination string, weightTon, volumeCbm float64) map[string]any {
	chargeable := math.Max(weightTon, volumeCbm*0.33)
	avg := round2f(600.0 + chargeable*180.0)
	return map[string]any{
		"source": "offline", "provider": "运满满(离线参考)",
		"route": orDash(origin) + "→" + orDash(destination),
		"low":   round2f(avg * 0.9), "avg": avg, "high": round2f(avg * 1.15),
		"currency": "CNY",
		"note":     "未配置运满满凭证或暂不可达，返回离线参考价。",
	}
}

// round2f 复刻 Python round(x, 2)：十进制半偶入
func round2f(v float64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 2, 64), 64)
	return f
}

// FreightQuote 调车比价：市场信号（离线或平台）+ 本线路近 90 天历史成交价混合。
//
// 未配置凭证时不外呼，直接给离线参考价——报价链路不该因为一个没接的外部服务而卡住。
func (h *Handler) FreightQuote(ctx context.Context, origin, destination string, weightTon, volumeCbm float64) map[string]any {
	// 平台侧未接入：Go 版与 Django 的「未配置」分支一致，直接走离线估算
	quote := offlineEstimate(origin, destination, weightTon, volumeCbm)

	var histAvg *float64
	_ = h.DB.QueryRow(ctx, `
		SELECT avg(quoted_amount)::float8 FROM ops_order
		WHERE NOT is_deleted AND status='completed' AND quoted_amount > 0
		  AND origin ILIKE '%'||$1||'%' AND destination ILIKE '%'||$2||'%'
		  AND created_at >= now() - interval '90 days'`, origin, destination).Scan(&histAvg)

	marketAvg, _ := quote["avg"].(float64)
	if histAvg != nil && marketAvg > 0 {
		blended := round2f(0.6*(*histAvg) + 0.4*marketAvg)
		quote["avg"] = blended
		quote["low"] = round2f(blended * 0.9)
		quote["high"] = round2f(blended * 1.12)
		quote["note"] = fmt.Sprintf("混合估值：历史成交价 %s元×0.6 + 离线参考价 %s元×0.4",
			pyFloat(*histAvg), pyFloat(marketAvg))
	} else {
		quote["note"] = quote["note"].(string) + " (无本地历史数据，纯市场估值)"
	}
	return quote
}

// pyFloat 复刻 Python str(float)：整数值也带 .0，Decimal 平均值带两位
func pyFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if s == strconv.FormatFloat(math.Trunc(f), 'g', -1, 64) && !hasDot(s) {
		return s + ".0"
	}
	return s
}

func hasDot(s string) bool {
	for _, c := range s {
		if c == '.' || c == 'e' || c == 'E' {
			return true
		}
	}
	return false
}

// YmmQuote GET /api/v1/orders/{id}/ymm-quote
func (h *Handler) YmmQuote(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.view") {
		return
	}
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var origin, destination string
	var weight, volume float64
	if err := h.DB.QueryRow(ctx, `
		SELECT origin, destination, COALESCE(cargo_weight_ton,0)::float8, COALESCE(cargo_volume_cbm,0)::float8
		FROM ops_order WHERE id=$1::uuid`, id).Scan(&origin, &destination, &weight, &volume); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取订单失败")
		return
	}
	httpx.JSON(w, http.StatusOK, h.FreightQuote(ctx, origin, destination, weight, volume))
}

// Convert POST /api/v1/orders/{id}/convert —— 订单转运单（不带承运商/车辆的最简转换）
func (h *Handler) Convert(w http.ResponseWriter, r *http.Request) {
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
	var status string
	_ = h.DB.QueryRow(ctx, `SELECT status FROM ops_order WHERE id=$1::uuid`, id).Scan(&status)
	switch status {
	case "converted", "completed":
		httpx.Err(w, http.StatusConflict, "ORDER_ALREADY_CONVERTED", "订单已派单/完成。")
		return
	case "cancelled":
		httpx.Err(w, http.StatusConflict, "ORDER_CANCELLED", "订单已取消。")
		return
	}
	var waybillNo string
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		no, err := nextNo(ctx, tx, "YD")
		if err != nil {
			return err
		}
		waybillNo = no
		wid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, order_id, customer_id,
			  route_name, origin, destination, cargo_quantity, cargo_weight_ton, cargo_volume_cbm,
			  status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
			  dispatch_type, ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term,
			  platform_name, platform_order_no, organization_id, planned_arrival)
			SELECT $1::uuid, now(), now(), $2, o.id, o.customer_id,
			  COALESCE(NULLIF(o.origin,''),'?')||'→'||COALESCE(NULLIF(o.destination,''),'?'),
			  o.origin, o.destination, o.cargo_quantity, o.cargo_weight_ton, o.cargo_volume_cbm,
			  'pending_dispatch', 'pending_accept', 'none', 'not_due', 0,
			  '', '', o.cod_amount, o.cod_status, o.freight_payer, o.freight_term,
			  '', '', u.organization_id, o.expected_delivery_at
			FROM ops_order o LEFT JOIN accounts_user u ON u.id = o.created_by_id
			WHERE o.id = $3::uuid`, wid.String(), no, id); err != nil {
			return err
		}
		// 站点整体拷贝进执行层（计划时间落 planned_eta，实到由围栏/手动盖戳）
		if _, err := tx.Exec(ctx, `
			INSERT INTO ops_waybill_stop (id, created_at, updated_at, waybill_id, seq, stop_type,
			  city, address, contact_name, contact_phone, planned_eta, status, note,
			  radius_m, arrival_source)
			SELECT gen_random_uuid(), now(), now(), $1::uuid, s.seq, s.stop_type,
			  s.city, s.address, s.contact_name, s.contact_phone,
			  COALESCE(s.expected_end, s.expected_start), 'pending', s.cargo_note, 800, ''
			FROM ops_order_stop s WHERE s.order_id = $2::uuid ORDER BY s.seq`, wid.String(), id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE ops_order SET status='converted', updated_at=now() WHERE id=$1::uuid`, id); err != nil {
			return err
		}
		return txEvent(ctx, tx, id, "converted", status, "converted", me.ID, "ops",
			map[string]any{"waybill_no": no})
	})
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "转运单失败："+err.Error())
		return
	}
	it, err := waybills.SerializeByNo(ctx, h.DB, waybillNo)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, it)
}
