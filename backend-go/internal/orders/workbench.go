package orders

// 个人工作台：GET /workbench —— 对齐 apps/ops/views.WorkbenchView
// 「我的待办」按角色聚合：通知/异常/客服/调度/财务四块。

import (
	"net/http"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// Workbench GET /api/v1/workbench
func (h *Handler) Workbench(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	isChief, _ := h.isChiefDispatcher(ctx, me)

	var unread, myOpenExc, myPendingCount, myToday, poolCount, myClaimed, draftStmts int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ntf_notification WHERE recipient_id=$1::uuid AND NOT is_read`, me.ID).Scan(&unread)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_exception
		WHERE assignee_id=$1::uuid AND status NOT IN ('closed','rejected')`, me.ID).Scan(&myOpenExc)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND created_by_id=$1::uuid AND status='pending_confirm'`, me.ID).Scan(&myPendingCount)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND created_by_id=$1::uuid
		  AND (created_at AT TIME ZONE 'Asia/Shanghai')::date = (now() AT TIME ZONE 'Asia/Shanghai')::date`, me.ID).Scan(&myToday)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order WHERE NOT is_deleted AND status='pooled'`).Scan(&poolCount)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND claimed_by_id=$1::uuid AND status='dispatching'`, me.ID).Scan(&myClaimed)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM fin_statement WHERE status='draft'`).Scan(&draftStmts)

	listOrders := func(where, orderBy string, args ...any) []map[string]any {
		rows, err := h.DB.Query(ctx, selectOrderSQL+fromClause+" WHERE NOT o.is_deleted AND "+where+" "+orderBy+" LIMIT 5", args...)
		if err != nil {
			return []map[string]any{}
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			if it, err := scanOrder(rows, me.ID, isChief); err == nil {
				out = append(out, it)
			}
		}
		return out
	}
	recentPending := listOrders("o.created_by_id=$1::uuid AND o.status='pending_confirm'", "ORDER BY o.created_at DESC", me.ID)
	poolTop := listOrders("o.status='pooled'", "ORDER BY o.priority DESC, o.pooled_at ASC")

	httpx.JSON(w, http.StatusOK, map[string]any{
		"common": map[string]any{
			"unread_notifications": unread,
			"my_open_exceptions":   myOpenExc,
		},
		"cs": map[string]any{
			"my_orders_pending_confirm": myPendingCount,
			"my_orders_today":           myToday,
			"recent_pending":            recentPending,
		},
		"dispatch": map[string]any{
			"pool_count": poolCount,
			"my_claimed": myClaimed,
			"pool_top":   poolTop,
		},
		"finance": map[string]any{
			"draft_statements": draftStmts,
		},
	})
}
