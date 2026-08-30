package exceptions

// 异常域：GET/POST /exceptions + POST /orders/{id}/report-exception。
// 对齐 ExceptionViewSet（org_field=waybill__organization）与 report_exception。
// assign/timeline/AI 诊断等处置动作低频，仍由代理提供。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
	MD  *masterdata.Handler
}

var exceptionTypeLabel = map[string]string{
	"transit_delay": "在途超时", "route_deviation": "偏航/路线异常", "cargo_damage": "货损货差",
	"vehicle_breakdown": "车辆故障", "detained": "扣车扣货", "customer_complaint": "客户投诉",
	"temperature": "冷链温度异常", "fuel": "油耗/漏油异常", "overspeed": "超速驾驶",
	"fatigue": "疲劳驾驶", "deviation": "偏航（车联网）", "abnormal_stop": "异常停车",
	"geofence": "围栏进出", "offline": "设备离线", "receipt_pending": "回单待确认", "other": "其他",
}

var excCfg = masterdata.ResourceCfg{
	SelectSQL: `
SELECT x.id::text AS id, x.waybill_id::text AS waybill, COALESCE(w.waybill_no,'') AS waybill_no,
       x.exception_type, x.level, x.source, x.description, x.status,
       x.assignee_id::text AS assignee, COALESCE(u.username,'') AS assignee_name,
       x.responsibility_party, x.amount::text AS amount, x.resolution, x.created_at`,
	FromClause: `FROM ops_exception x
LEFT JOIN ops_waybill w ON w.id = x.waybill_id
LEFT JOIN accounts_user u ON u.id = x.assignee_id`,
	SearchCols: []string{"x.exception_type", "x.description"},
	OrderingCols: map[string]string{
		"created_at": "x.created_at",
	},
	DirectParams: map[string]string{
		"exception_type": "x.exception_type", "status": "x.status", "level": "x.level",
		"source": "x.source", "waybill": "x.waybill_id::text",
	},
	DefaultOrder: "ORDER BY x.created_at DESC, x.id",
}

// List GET /api/v1/exceptions（数据范围按运单组织归属）
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	// 这条路由**覆盖掉了**通用 CRUD 挂好的那条（main.go 里
	// mdH.CRUD(...) 之后又写了 rt.Get("/", excH.List)），
	// 于是 CRUD 上 ReadPerm: "waybill.view" 那道闸一起被盖掉了。
	// 自实现的这份只做了数据范围——而范围管的是"看得见谁的单"，
	// 不是"该不该看异常这一面"。实测一个只有 masterdata.view、
	// 数据范围给"全部"的账号，打这里拿得到异常记录（含责任方、
	// 赔付金额、处理结论）。
	if !h.MD.Allow(w, r, "waybill.view") {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	cfg := excCfg
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			cfg.FromClause += " JOIN ops_waybill sw ON sw.id = x.waybill_id AND false"
		} else {
			quoted := make([]string, len(scopeIDs))
			for i, id := range scopeIDs {
				quoted[i] = "'" + strings.ReplaceAll(id, "'", "") + "'"
			}
			cfg.FromClause += fmt.Sprintf(" JOIN ops_waybill sw ON sw.id = x.waybill_id AND sw.organization_id::text IN (%s)", strings.Join(quoted, ","))
		}
	}
	h.MD.List(w, r, cfg)
}

// nilIfEmpty 空字符串转 NULL，避免把 ” 送进 uuid 列
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (h *Handler) excEvent(ctx context.Context, excID, eventType, toStatus, actorID, note, source string) {
	eid, _ := uuid.NewV7()
	pj, _ := json.Marshal(map[string]any{"source": source})
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO ops_exception_event (id, created_at, updated_at, exception_id, event_type,
		  from_status, to_status, actor_id, note, payload, event_time)
		VALUES ($1, now(), now(), $2::uuid, $3, '', $4, $5::uuid, $6, $7, clock_timestamp())`,
		eid.String(), excID, eventType, toStatus, actorID, note, pj); err != nil {
		slog.Warn("异常事件写库失败", "err", err)
	}
}

// Create POST /api/v1/exceptions —— 挂运单登记（运单详情页上报）
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	// 上报异常按 waybill.view 放行而不是 waybill.manage：发现问题的常常是客服，
	// 而他们只有查看权。**登记问题的门要低，定责赔钱的门要高**——
	// 后面 Assign/Handle/Close 三个动作要的是 waybill.manage。
	if !h.MD.Allow(w, r, "waybill.view") {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	details := map[string]any{}
	// waybill 可空（模型 null=True）：不挂运单的异常也是合法登记
	waybillID, _ := body["waybill"].(string)
	if waybillID == "" { //nolint:staticcheck — 保留空分支以对齐 Django 的"可空即跳过校验"
	} else if _, err := uuid.Parse(waybillID); err != nil {
		details["waybill"] = []string{"“" + waybillID + "” 不是合法 UUID。"}
	} else {
		var exists bool
		_ = h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ops_waybill WHERE id=$1::uuid)`, waybillID).Scan(&exists)
		if !exists {
			details["waybill"] = []string{"运单不存在。"}
		}
	}
	excType, _ := body["exception_type"].(string)
	if excType == "" {
		details["exception_type"] = []string{"该字段是必填项。"}
	} else if exceptionTypeLabel[excType] == "" {
		details["exception_type"] = []string{"“" + excType + "” 不是合法选项。"}
	}
	if len(details) > 0 {
		httpx.ErrDetails(w, http.StatusBadRequest, "invalid", "请求参数校验失败", details)
		return
	}
	level, _ := body["level"].(string)
	if level == "" {
		level = "medium"
	}
	source, _ := body["source"].(string)
	if source == "" {
		source = "manual"
	}
	desc, _ := body["description"].(string)
	id, _ := uuid.NewV7()
	_, err = h.DB.Exec(ctx, `
		INSERT INTO ops_exception (id, created_at, updated_at, waybill_id, order_id, reported_by_id,
		  exception_type, level, source, description, status, responsibility_party, amount, resolution)
		VALUES ($1, now(), now(), $2::uuid, (SELECT order_id FROM ops_waybill WHERE id=$2::uuid),
		  $3::uuid, $4, $5, $6, $7, 'pending_handle', '', 0, '')`,
		id.String(), nilIfEmpty(waybillID), me.ID, excType, level, source, desc)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	h.excEvent(ctx, id.String(), "create", "pending_handle", me.ID, desc, source)
	it, err := h.MD.One(ctx, excCfg, "x.id = $1::uuid", id.String())
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, it)
}

// ReportForOrder POST /api/v1/orders/{id}/report-exception —— 订单池登记 + 订单事件
func (h *Handler) ReportForOrder(w http.ResponseWriter, r *http.Request) {
	// 与 Create 同一道门：发现问题的常常是只有查看权的客服
	if !h.MD.Allow(w, r, "waybill.view") {
		return
	}
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	orderID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(orderID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	var orderNo string
	if err := h.DB.QueryRow(ctx, `SELECT order_no FROM ops_order WHERE id=$1::uuid AND NOT is_deleted`, orderID).Scan(&orderNo); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	excType, _ := body["exception_type"].(string)
	if excType == "" {
		excType = "other"
	}
	if exceptionTypeLabel[excType] == "" {
		httpx.Err(w, http.StatusBadRequest, "INVALID_EXCEPTION_TYPE", "异常类型非法。")
		return
	}
	level, _ := body["level"].(string)
	if level == "" {
		level = "medium"
	}
	desc, _ := body["description"].(string)
	desc = strings.TrimSpace(desc)
	id, _ := uuid.NewV7()
	// waybill = order.waybills.first()（默认排序 -created_at 之外：Waybill 无 Meta ordering
	// 时 first() 按插入序 —— 取最早创建的一张，与 Django .first() 的主键序一致）
	_, err = h.DB.Exec(ctx, `
		INSERT INTO ops_exception (id, created_at, updated_at, waybill_id, order_id, reported_by_id,
		  exception_type, level, source, description, status, responsibility_party, amount, resolution)
		VALUES ($1, now(), now(),
		  (SELECT id FROM ops_waybill WHERE order_id=$2::uuid ORDER BY created_at, id LIMIT 1),
		  $2::uuid, $3::uuid, $4, $5, 'manual', $6, 'pending_handle', '', 0, '')`,
		id.String(), orderID, me.ID, excType, level, desc)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "写入失败")
		return
	}
	// 订单事件（record_order_event: exception_reported + note）
	oe, _ := uuid.NewV7()
	pj, _ := json.Marshal(map[string]any{"note": "登记异常：" + exceptionTypeLabel[excType]})
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO ops_order_event (id, created_at, updated_at, event_time, order_id, event_type,
		  from_status, to_status, actor_id, source, payload)
		VALUES ($1, now(), now(), clock_timestamp(), $2::uuid, 'exception_reported', '', '', $3::uuid, 'exception', $4)`,
		oe.String(), orderID, me.ID, pj); err != nil {
		slog.Warn("异常事件写库失败", "err", err)
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id": id.String(), "order_no": orderNo, "exception_type": excType, "level": level,
	})
}
