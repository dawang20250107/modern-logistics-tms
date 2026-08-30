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
	// 个人计数（我的待确认 / 我认领 / 我的异常 / 未读通知）天然按 me.ID 收窄，没问题。
	// 但「池中待派」与「草稿对账单」是全局计数，原先谁都能看见——
	// 前者还会连订单明细一起给出来（pool_top 是完整记录，不是数字）。
	// 数据范围要和 /orders 列表一致：按建单人所属组织。
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	poolScope, poolArgs := "true", []any{}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			poolScope = "false"
		} else {
			poolScope = "cb.organization_id::text = ANY($1)"
			poolArgs = append(poolArgs, scopeIDs)
		}
	}
	// 对账草稿数属于财务域：没有 finance.view 就不该在工作台上看见它
	_, _, myPerms, _ := h.Svc.RolesAndPerms(ctx, me)
	canFinance := auth.HasPerm(myPerms, "finance.view")
	// 同理，池中待派属于订单域。这里原先只收了数据范围、没判权限点：
	// 一个只有 masterdata.view（"主数据查看"）、数据范围给了"全部"的角色，
	// 打开工作台就看到 pool_count=4588 和 pool_top 里五条完整订单记录，
	// 而它打 /orders 列表是规规矩矩 403 的——同一份数据，换个入口就出来了。
	// 数据范围管的是"看得见谁的单"，管不了"该不该看订单这一面"。
	canWaybill := auth.HasPerm(myPerms, "waybill.view")
	if !canWaybill {
		poolScope, poolArgs = "false", nil
	}

	var unread, myOpenExc, myPendingCount, myToday, poolCount, myClaimed, draftStmts int
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ntf_notification WHERE recipient_id=$1::uuid AND NOT is_read`, me.ID).Scan(&unread)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_exception
		WHERE assignee_id=$1::uuid AND status NOT IN ('closed','rejected')`, me.ID).Scan(&myOpenExc)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND created_by_id=$1::uuid AND status='pending_confirm'`, me.ID).Scan(&myPendingCount)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND created_by_id=$1::uuid
		  AND (created_at AT TIME ZONE 'Asia/Shanghai')::date = (now() AT TIME ZONE 'Asia/Shanghai')::date`, me.ID).Scan(&myToday)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order o
		LEFT JOIN accounts_user cb ON cb.id = o.created_by_id
		WHERE NOT o.is_deleted AND o.status='pooled' AND `+poolScope, poolArgs...).Scan(&poolCount)
	_ = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND claimed_by_id=$1::uuid AND status='dispatching'`, me.ID).Scan(&myClaimed)
	if canFinance {
		fargs := []any{}
		fscope := "true"
		if scopeIDs != nil {
			if len(scopeIDs) == 0 {
				fscope = "false"
			} else {
				// 跨组织对账单 organization_id 为 NULL，只有 all 档看得见（见 005 迁移）
				fscope = "organization_id::text = ANY($1)"
				fargs = append(fargs, scopeIDs)
			}
		}
		_ = h.DB.QueryRow(ctx,
			`SELECT count(*) FROM fin_statement WHERE status='draft' AND `+fscope, fargs...).Scan(&draftStmts)
	}

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
	// poolScope 里的 $1 与 poolArgs 一一对应（listOrders 只传这一组参数）
	poolTop := listOrders("o.status='pooled' AND "+poolScope,
		"ORDER BY o.priority DESC, o.pooled_at ASC", poolArgs...)

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
