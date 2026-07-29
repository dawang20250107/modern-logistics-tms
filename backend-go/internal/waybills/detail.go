package waybills

// 运单详情读：GET /waybills/{no} —— WaybillDetailSerializer =
// 列表列面 + stops + timeline(events) + agent_suggestions + next_statuses。

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

var stopTypeLabel = map[string]string{"pickup": "提货", "delivery": "送货"}
var stopStatusLabel = map[string]string{"pending": "待到达", "arrived": "已到达", "departed": "已离开"}

// Detail GET /api/v1/waybills/{no}
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, "waybill.view") {
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无运单查看权限")
		return
	}
	no := chi.URLParam(r, "no")
	args := &filters.Args{}
	where := "WHERE w.waybill_no = " + args.Add(no)
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			httpx.Err(w, http.StatusNotFound, "error", "No Waybill matches the given query.")
			return
		}
		where += fmt.Sprintf(" AND w.organization_id::text = ANY(%s)", args.Add(scopeIDs))
	}

	rows, err := h.DB.Query(ctx, selectWaybillSQL+fromClause+" "+where, args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		httpx.Err(w, http.StatusNotFound, "error", "No Waybill matches the given query.")
		return
	}
	it, err := scanWaybill(rows)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取行失败")
		return
	}
	rows.Close()
	wbID := it["id"].(string)

	var stops, timeline, suggestions json.RawMessage
	if err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(json_agg(json_build_object(
		    'id', s.id::text, 'seq', s.seq, 'stop_type', s.stop_type,
		    'city', s.city, 'address', s.address,
		    'contact_name', s.contact_name, 'contact_phone', s.contact_phone,
		    'lat', s.lat::text, 'lng', s.lng::text, 'radius_m', s.radius_m,
		    'planned_eta', s.planned_eta, 'actual_arrival_at', s.actual_arrival_at,
		    'actual_depart_at', s.actual_depart_at, 'arrival_source', s.arrival_source,
		    'status', s.status, 'note', s.note
		  ) ORDER BY s.seq, s.id), '[]'::json)
		FROM ops_waybill_stop s WHERE s.waybill_id = $1::uuid`, wbID).Scan(&stops); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	if err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(json_agg(json_build_object(
		    'id', e.id::text, 'event_type', e.event_type, 'event_time', e.event_time,
		    'resource', e.resource, 'source', e.source, 'payload', e.payload
		  ) ORDER BY e.event_time), '[]'::json)
		FROM ops_waybill_event e WHERE e.waybill_id = $1::uuid`, wbID).Scan(&timeline); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	if err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(json_agg(json_build_object(
		    'id', g.id::text, 'suggestion_type', g.suggestion_type, 'title', g.title,
		    'body', g.body, 'status', g.status, 'evidence', g.evidence, 'created_at', g.created_at
		  ) ORDER BY g.created_at DESC), '[]'::json)
		FROM ai_agent_suggestion g WHERE g.waybill_id = $1::uuid`, wbID).Scan(&suggestions); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}

	// stops 标签中文化（get_stop_type_display / get_status_display）
	var rawStops []map[string]any
	_ = json.Unmarshal(stops, &rawStops)
	outStops := make([]map[string]any, 0, len(rawStops))
	for _, s := range rawStops {
		st, _ := s["stop_type"].(string)
		ss, _ := s["status"].(string)
		s["stop_type_label"] = stopTypeLabel[st]
		s["status_label"] = stopStatusLabel[ss]
		outStops = append(outStops, s)
	}

	it["stops"] = outStops
	it["timeline"] = timeline
	it["agent_suggestions"] = suggestions
	next := allowedTransitions[it["status"].(string)]
	if next == nil {
		next = []string{}
	}
	it["next_statuses"] = next
	httpx.JSON(w, http.StatusOK, it)
}
