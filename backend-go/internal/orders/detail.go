package orders

// 订单详情读：GET /orders/{id}（复用 OrderSerializer 列面）+ GET /orders/{id}/timeline。
// workflow 仍由 Django 代理（apps/ops/workflow.py 属聚合视图，后续 workbench 域一并移植）。

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// Detail GET /api/v1/orders/{id} —— 与列表同一序列化（Django retrieve 亦复用 OrderSerializer）
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	args := &filters.Args{}
	where := "WHERE NOT o.is_deleted AND o.id = " + args.Add(id) + "::uuid"
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
			return
		}
		where += fmt.Sprintf(" AND cb.organization_id::text = ANY(%s)", args.Add(scopeIDs))
	}
	isChief, _ := h.isChiefDispatcher(ctx, me)
	rows, err := h.DB.Query(ctx, selectOrderSQL+fromClause+" "+where, args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	it, err := scanOrder(rows, me.ID, isChief)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取行失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

// Timeline GET /api/v1/orders/{id}/timeline —— OrderEventSerializer，event_time 升序
func (h *Handler) Timeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	// 与 Detail 相同的可见性校验（get_object 走同一 queryset）
	args := &filters.Args{}
	where := "WHERE NOT o.is_deleted AND o.id = " + args.Add(id) + "::uuid"
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
			return
		}
		where += fmt.Sprintf(" AND cb.organization_id::text = ANY(%s)", args.Add(scopeIDs))
	}
	var visible bool
	if err := h.DB.QueryRow(ctx, "SELECT true "+fromClause+" "+where, args.Values...).Scan(&visible); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	var eventsJSON json.RawMessage
	err = h.DB.QueryRow(ctx, `
		SELECT COALESCE(json_agg(json_build_object(
		    'id', e.id::text, 'event_type', e.event_type,
		    'from_status', e.from_status, 'to_status', e.to_status,
		    'actor_name', COALESCE(u.username, ''),
		    'source', e.source, 'payload', e.payload, 'event_time', e.event_time
		  ) ORDER BY e.event_time), '[]'::json)
		FROM ops_order_event e LEFT JOIN accounts_user u ON u.id = e.actor_id
		WHERE e.order_id = $1::uuid`, id).Scan(&eventsJSON)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	httpx.JSON(w, http.StatusOK, eventsJSON)
}
