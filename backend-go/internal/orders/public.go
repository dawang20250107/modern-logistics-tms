package orders

// 公开域（免登录）：客户自助下单 + 自助订单跟踪。
//
// 两个端点都无鉴权，因此各自带一道自证机制：
//   - 下单落的是「待确认」订单，进客服确认队列，不会直接进入调度；
//   - 跟踪必须「订单号 + 手机号」同时匹配才返回，且只返回脱敏进度
//     （状态中文名、里程碑、最新车辆位置），不暴露客户、承运商、金额。

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// PublicIntake POST /api/v1/public/orders —— 客户自助下单（免登录）
func (h *Handler) PublicIntake(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body == nil {
		body = map[string]any{}
	}
	contactPhone := strings.TrimSpace(str(body, "contact_phone"))
	origin := strings.TrimSpace(str(body, "origin"))
	destination := strings.TrimSpace(str(body, "destination"))
	if contactPhone == "" || origin == "" || destination == "" {
		httpx.Err(w, http.StatusBadRequest, "PUBLIC_INTAKE_REQUIRED", "联系电话、始发、目的地必填。")
		return
	}
	channel := str(body, "channel")
	if channel != "self" && channel != "miniprogram" {
		channel = "self"
	}
	source := str(body, "source")
	if source == "" {
		source = "客户自助"
	}
	data := map[string]any{
		"contact_name":       str(body, "contact_name"),
		"contact_phone":      contactPhone,
		"origin":             origin,
		"destination":        destination,
		"cargo_desc":         str(body, "cargo_desc"),
		"cargo_weight_ton":   orZero(body["cargo_weight_ton"]),
		"cargo_quantity":     orZero(body["cargo_quantity"]),
		"expected_pickup_at": body["expected_pickup_at"],
		"remark":             str(body, "remark"),
		"source_type":        "individual",
	}
	customerID := h.matchCustomer(ctx, data, channel, source, "")
	enrich(data)

	orderID, code, msg := h.createOrder(ctx, createParams{
		Data: data, Channel: channel, Source: source, Status: "pending_confirm",
		CustomerID: customerID, ParseMeta: map[string]any{},
	})
	if code != "" {
		httpx.Err(w, http.StatusInternalServerError, code, msg)
		return
	}
	var orderNo, status string
	if err := h.DB.QueryRow(ctx,
		"SELECT order_no, status FROM ops_order WHERE id=$1::uuid", orderID).Scan(&orderNo, &status); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"order_no": orderNo, "status": status,
		"message": "下单成功，客服将尽快与您确认。可凭订单号与手机号查询进度。",
	})
}

func orZero(v any) any {
	if v == nil || v == "" {
		return 0
	}
	return v
}

// trackedEvents 对外只披露主干里程碑，内部流转（认领/释放/改单等）不出现在公开跟踪里
var trackedEvents = []string{"created", "confirmed", "pooled", "dispatched", "completed"}

// PublicTrack GET /api/v1/track?order_no=&phone= —— 客户自助跟踪（免登录）
func (h *Handler) PublicTrack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	orderNo := strings.TrimSpace(q.Get("order_no"))
	phone := strings.TrimSpace(q.Get("phone"))
	if orderNo == "" || phone == "" {
		httpx.Err(w, http.StatusBadRequest, "TRACK_PARAMS", "order_no 与 phone 必填。")
		return
	}
	// 手机号校验：全等或「输入至少 4 位且为登记号码的后缀」，三个联系号任一命中即可
	var orderID, status, businessType, ordOrigin, ordDest string
	var createdAt time.Time
	err := h.DB.QueryRow(ctx, `
		SELECT o.id::text, o.status, o.business_type, o.origin, o.destination, o.created_at
		FROM ops_order o
		WHERE o.order_no = $1 AND (
		    o.contact_phone = $2 OR o.pickup_contact_phone = $2 OR o.delivery_contact_phone = $2
		    OR (length($2) >= 4 AND (
		        (o.contact_phone <> '' AND o.contact_phone LIKE '%' || $2)
		     OR (o.pickup_contact_phone <> '' AND o.pickup_contact_phone LIKE '%' || $2)
		     OR (o.delivery_contact_phone <> '' AND o.delivery_contact_phone LIKE '%' || $2))))
		LIMIT 1`, orderNo, phone).
		Scan(&orderID, &status, &businessType, &ordOrigin, &ordDest, &createdAt)
	if err != nil {
		httpx.Err(w, http.StatusNotFound, "TRACK_NOT_FOUND", "未找到匹配的订单，请核对订单号与手机号。")
		return
	}

	milestones := []map[string]any{}
	rows, err := h.DB.Query(ctx, `
		SELECT event_type, event_time FROM ops_order_event
		WHERE order_id=$1::uuid AND event_type = ANY($2)
		ORDER BY event_time, id`, orderID, trackedEvents)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var et string
			var at time.Time
			if rows.Scan(&et, &at) != nil {
				break
			}
			milestones = append(milestones, map[string]any{"event": et, "time": pyISO(at)})
		}
	}

	var shipment any
	var wbNo, wbStatus, receiptStatus string
	var eta *time.Time
	var vehicleID *string
	if err := h.DB.QueryRow(ctx, `
		SELECT waybill_no, status, receipt_status, estimated_arrival, vehicle_id::text
		FROM ops_waybill WHERE order_id=$1::uuid ORDER BY created_at DESC, id LIMIT 1`, orderID).
		Scan(&wbNo, &wbStatus, &receiptStatus, &eta, &vehicleID); err == nil {
		var position any
		if vehicleID != nil {
			var lat, lng float64
			var at time.Time
			if h.DB.QueryRow(ctx, `
				SELECT lat::float8, lng::float8, reported_at FROM tel_vehicle_state
				WHERE vehicle_id=$1::uuid AND reported_at IS NOT NULL
				ORDER BY reported_at DESC, id LIMIT 1`, *vehicleID).Scan(&lat, &lng, &at) == nil {
				position = map[string]any{"lat": lat, "lng": lng, "at": pyISO(at)}
			}
		}
		var etaOut any
		if eta != nil {
			etaOut = pyISO(*eta)
		}
		shipment = map[string]any{
			"waybill_no": wbNo, "status": labelOr(waybillStatusLabels, wbStatus),
			"estimated_arrival": etaOut, "receipt_status": receiptStatus, "position": position,
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"order_no":      orderNo,
		"status":        labelOr(orderStatusLabels, status),
		"business_type": labelOr(businessTypeLabels, businessType),
		"origin":        ordOrigin,
		"destination":   ordDest,
		"created_at":    pyISO(createdAt),
		"milestones":    milestones,
		"shipment":      shipment,
	})
}

func labelOr(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

// pyISO 复刻 Python datetime.isoformat()（微秒定长 6 位，为 0 时不带小数）。
// 注意公开跟踪走的是 isoformat() 而非 DRF 序列化器，所以 +00:00 不会被换成 Z。
func pyISO(t time.Time) string {
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00")
}

var orderStatusLabels = map[string]string{
	"draft": "草稿", "pending_confirm": "待确认", "confirmed": "已确认", "pooled": "订单池",
	"dispatching": "调度中", "converted": "已派单", "completed": "已完成", "cancelled": "已取消",
}

var businessTypeLabels = map[string]string{
	"ftl": "整车", "ltl": "零担", "express": "快递", "coldchain": "冷链", "hazmat": "危化",
}

var waybillStatusLabels = map[string]string{
	"draft": "草稿", "pending_dispatch": "待调度", "dispatched": "已派车", "loaded": "已装车",
	"departed": "已发车", "in_transit": "运输中", "arrived": "已到达", "partially_signed": "部分签收",
	"rejected": "已拒收", "signed": "已签收", "delivered": "已送达", "settled": "已结算",
	"cancelled": "已取消", "voided": "已作废",
}
