package orders

// 公开域（免登录）：客户自助下单 + 自助订单跟踪。
//
// 两个端点都无鉴权，因此各自带一道自证机制：
//   - 下单落的是「待确认」订单，进客服确认队列，不会直接进入调度；
//   - 跟踪必须「订单号 + 手机号」同时匹配才返回，且只返回脱敏进度
//     （状态中文名、里程碑、最新车辆位置），不暴露客户、承运商、金额。

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

// PublicIntake POST /api/v1/public/orders —— 客户自助下单（免登录）
// 对公开放端点的限流。速率可用环境变量覆盖（部署侧要按真实流量调）。
var (
	// 按订单号：同一个单号被反复试就是暴破信号。10/min 意味着穷举
	// 10000 种四位后缀要 1000 分钟以上，而且全程会在 /metrics 上留下
	// 大量 404 与 429，能被告警发现。
	trackByOrderThrottle = httpx.NewThrottle("THROTTLE_TRACK_ORDER", "10/min")
	// 按来源 IP，但**只在查不到时**计数。正常客户一次就查到，不占配额；
	// 横向扫描的人几乎全是查不到。20 次失败/分钟对手滑输错的人足够宽松。
	trackFailByIPThrottle = httpx.NewThrottle("THROTTLE_TRACK_FAIL_IP", "20/min")
	// 公开建单：真实客户一次提交一单，5/min 足够宽松。
	intakeThrottle = httpx.NewThrottle("THROTTLE_PUBLIC_INTAKE", "5/min")
)

func (h *Handler) PublicIntake(w http.ResponseWriter, r *http.Request) {
	// 免登录建单：不挡的话，任何人都能把客服工作台灌满垃圾单，
	// 而客服无法区分哪些是真客户提交的。
	if !intakeThrottle.Guard(w, r) {
		return
	}
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
		"expected_pickup_at": body["expected_pickup_at"],
		"remark":             str(body, "remark"),
		"source_type":        "individual",
	}
	// 数值字段先校验再落库：填错格子的客户该拿到 400 和"哪一格错了"，
	// 而不是 500 和一段 Postgres 报错（详见 numOrErr）。
	for _, f := range []struct{ key, label string }{
		{"cargo_weight_ton", "重量(吨)"},
		{"cargo_quantity", "件数"},
		{"cargo_volume_cbm", "体积(方)"},
	} {
		v, bad := numOrErr(body[f.key], f.label)
		if bad != "" {
			httpx.Err(w, http.StatusBadRequest, "PUBLIC_INTAKE_INVALID", f.label+"请填数字。")
			return
		}
		data[f.key] = v
	}
	customerID := h.matchCustomer(ctx, data, channel, source, "")
	enrich(data)

	orderID, code, msg := h.createOrder(ctx, createParams{
		Data: data, Channel: channel, Source: source, Status: "pending_confirm",
		CustomerID: customerID, ParseMeta: map[string]any{},
	})
	if code != "" {
		// **不把内部错误原文回给匿名调用方。**
		// createOrder 的 msg 里带的是 Postgres 的原始报错（引擎、列类型、SQLSTATE），
		// 那是给日志看的，不是给公网看的。实测泄露过这一句：
		//   建单失败：ERROR: invalid input syntax for type numeric: "三吨" (SQLSTATE 22P02)
		// 现在原文只进日志，回给客户的是一句能照着做的话。
		slog.Error("公开建单失败", "code", code, "err", msg,
			"ip", httpx.ClientIP(r), "phone", contactPhone)
		httpx.Err(w, http.StatusInternalServerError, "PUBLIC_INTAKE_FAILED",
			"下单失败，请稍后再试或直接联系客服。")
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

// numOrErr 把公开表单里的数值字段归一成数字；不是数字就说清楚是哪一格。
//
// 原先是 orZero：空值补 0，其余**原样往下传**。于是客户在"重量(吨)"里
// 填「三吨」时，那个字符串一路传到 INSERT，Postgres 报
//
//	invalid input syntax for type numeric: "三吨" (SQLSTATE 22P02)
//
// 而这条错误被原样塞进响应回给了**匿名用户**：HTTP 500，
// 消息里带着数据库引擎、列类型和 SQLSTATE。
// 界面上显示的则是「提交失败，请检查网络后重试」——指向一个错误的动作，
// 客户会反复重试，而重试一万次结果一样。
//
// 一个填错格子的客户应该拿到 400 和"重量请填数字"，不是 500 和一段 SQL 报错。
func numOrErr(v any, field string) (any, string) {
	switch x := v.(type) {
	case nil:
		return 0, ""
	case float64:
		return x, ""
	case json.Number:
		return x, ""
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return 0, ""
		}
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, field
		}
		return f, ""
	}
	return nil, field
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
	// 两道闸，缺一不可。
	//
	// 这个端点免登录、对整个互联网开放，而它的"密码"只是手机号后 4 位
	// （下面那条 SQL 允许 >=4 位的后缀匹配，这是有意的可用性取舍——
	// 客户常常只记得后四位）。订单号又是顺序可猜的。
	// 实测过：没有闸的时候，穷举 10000 种四位组合只要 **60 秒**，
	// 也就是任何人都能把全系统的发货信息（起止地、状态、时间线）爬走。
	// 对一家物流公司来说，那是把客户的发货规律直接送给同行。
	//
	// 按订单号的闸挡定向暴破：被反复试的是那个单号，攻击者换多少 IP 都绕不开。
	// 按 IP 的闸挡横向扫描（拿一批单号各试几次），但**只对失败计数**——见下。
	if !trackByOrderThrottle.GuardKey(w, "no:"+orderNo) {
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
		// 只有**查不到**才消耗这个 IP 的配额。
		//
		// 第一版是在查询之前按 IP 无差别限流的，结果被自己的用例抓了出来：
		// 打满一个单号之后，另一个客户的正常查询也被连带挡住。
		// 现实里这一条更要命——国内大量用户共用出口 IP（企业 NAT、运营商 CGNAT），
		// 30/min 摊到成百上千人身上立刻就满，正常客户查不了自己的单。
		// 安全修复就这么变成了拒绝服务。
		//
		// 只对失败计数就没有这个问题：客户知道自己的手机号，一次就查到，
		// 永远不消耗配额；而横向扫描的人几乎全是查不到，很快就被切断。
		// 代价是每次失败仍会走一次库查询——那是一条走索引的单行查询，
		// 而同一单号的重复尝试已经被上面那道闸挡住了。
		if !trackFailByIPThrottle.Guard(w, r) {
			return
		}
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
			"waybill_no": wbNo, "status": labelOr(wbstatus.Label, wbStatus),
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
