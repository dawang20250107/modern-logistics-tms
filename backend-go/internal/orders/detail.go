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
	// 订单这一面的读，权限点是 waybill.view —— 前端导航上早就是这么声明的
	// （AppLayout 里「订单管理」「调度工作台」都写着 perm: "waybill.view"），
	// 后端这 9 条读路由却一条都没执行。
	//
	// 实测一个只有 masterdata.view（"主数据查看"，听起来只是看客户和司机档案）
	// 且数据范围给了"全部"的角色：
	//   GET /api/v1/orders          → 200，全库订单
	//   GET /api/v1/orders/export   → 200，5.26 MB、50002 行 CSV，
	//                                  客户名、始发目的、报价一次拉走
	//   GET /api/v1/orders/funnel   → 全库漏斗：cs 50839 单、self 1 单、各状态分布
	// 同一个账号打 /waybills、/statements、/reimbursements 都规规矩矩 403 ——
	// 订单是唯一漏的那一面，而它恰恰是数据量最大、最敏感的那一面。
	//
	// 数据范围挡不住这件事：范围管的是"看得见谁的单"，
	// 给了"全部"就等于全库。三个内置角色都带 waybill.view，补上不影响它们。
	if !h.allow(w, r, "waybill.view") {
		return
	}
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
	rows.Close()
	// 附件单查一次，只在详情里查。
	//
	// scanOrder 把 attachments 写死成空数组（列表页每行都去查附件是 N+1，
	// 列表也不显示附件，所以那样是对的）——但详情页也走同一个组装函数，
	// 于是这一栏永远是空的。前端整页只取 /orders/{id} 这一个接口，
	// 附件栏读的就是它，所以传上去的合同、磅单一律显示"暂无附件"：
	// 传成功了、库里也有、接口也 201，就是页面上看不见。
	// 另有一个 GET /orders/{id}/attachments 端点能查到，但没有任何页面调它。
	if list, aerr := h.childRows(ctx, attachmentSelect+
		" WHERE a.order_id=$1::uuid ORDER BY a.created_at, a.id", id); aerr == nil {
		it["attachments"] = normalizeAttachments(list)
	}
	httpx.JSON(w, http.StatusOK, it)
}

// Timeline GET /api/v1/orders/{id}/timeline —— OrderEventSerializer，event_time 升序
func (h *Handler) Timeline(w http.ResponseWriter, r *http.Request) {
	// 订单集合读补上 waybill.view 之后，**子资源这一半还开着**：
	// 一个只有 masterdata.view 的账号打 /orders 是 403，
	// 但换成 /orders/{id}/workflow、/timeline、/lineage、
	// /dispatch-suggestion、/ymm-quote 就照样 200 ——
	// 里面有单号、各阶段时间、推荐承运商、市场报价区间。
	// 拿单号不难：客户在邮件里给的、司机报的、或者顺手枚举。
	if !h.allow(w, r, "waybill.view") {
		return
	}
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
