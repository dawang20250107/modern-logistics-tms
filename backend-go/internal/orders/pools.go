package orders

// 订单池与调度台的只读端点：
//   GET /orders/pool?scope=free|mine|all   在池待派 + 调度中
//   GET /orders/dispatched?scope=mine|all  已转运单
//   GET /orders/dispatchers                可分派的调度成员
//   GET /orders/customer-addresses?customer= 客户地址簿
//   GET /orders/export                     当前筛选结果导出 CSV
//
// 对齐 OrderViewSet 的 pool_list / dispatched_list / dispatchers /
// customer_addresses / export。数据范围一律沿用列表口径（建单人组织子树）。

import (
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// scopeWhere 数据范围片段：nil 表示不收窄；空片表示一条都看不到
func (h *Handler) scopeWhere(r *http.Request, me *auth.UserRow, args *filters.Args) (string, error) {
	ids, err := h.Svc.ScopeOrgIDs(r.Context(), me)
	if err != nil {
		return "", err
	}
	if ids == nil {
		return "", nil
	}
	if len(ids) == 0 {
		return "false", nil
	}
	return fmt.Sprintf("cb.organization_id::text = ANY(%s)", args.Add(ids)), nil
}

// poolPage 池类端点共用的取数：额外条件 + 排序 + 分页 + 序列化
func (h *Handler) poolPage(w http.ResponseWriter, r *http.Request, extraWhere func(*filters.Args, *auth.UserRow, bool) []string, orderSQL string) {
	ctx := r.Context()
	q := r.URL.Query()
	page := clampInt(q.Get("page"), 1, 1, 1<<30)
	pageSize := clampInt(q.Get("page_size"), 20, 1, 200)

	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	isChief, _ := h.isChiefDispatcher(ctx, me)
	canViewAll, err := h.canViewAll(r, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}

	args := &filters.Args{}
	where := []string{"NOT o.is_deleted"}
	sw, err := h.scopeWhere(r, me, args)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if sw != "" {
		where = append(where, sw)
	}
	where = append(where, extraWhere(args, me, canViewAll)...)
	// 检索/筛选/仅看紧急交给库去做。以前这些全在前端对着取回来的那一页做，
	// 一页装不下就等于只在 20 条里搜——数据一多就悄悄变成了抽样。
	where = append(where, searchAndFilterWhere(q, args)...)
	if frag := filters.Apply(q.Get("filter"), filterFields, args); frag != "" {
		where = append(where, frag)
	}
	whereSQL := "WHERE " + strings.Join(where, " AND ")

	var total int
	if err := h.DB.QueryRow(ctx, "SELECT count(*) "+fromClause+" "+whereSQL, args.Values...).Scan(&total); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败："+err.Error())
		return
	}
	limitPh := args.Add(pageSize)
	offsetPh := args.Add((page - 1) * pageSize)
	rows, err := h.DB.Query(ctx, pagedOrderSQL(whereSQL, orderSQL, limitPh, offsetPh), args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败："+err.Error())
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, pageSize)
	for rows.Next() {
		it, err := scanOrder(rows, me.ID, isChief)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取行失败")
			return
		}
		items = append(items, it)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "page_size": pageSize,
		"pages": int(math.Max(1, math.Ceil(float64(total)/float64(pageSize)))),
	})
}

// canViewAll 对齐 views._can_view_all：超管或数据范围为 all
func (h *Handler) canViewAll(r *http.Request, me *auth.UserRow) (bool, error) {
	if me.IsSuperuser {
		return true, nil
	}
	ids, err := h.Svc.ScopeOrgIDs(r.Context(), me)
	if err != nil {
		return false, err
	}
	return ids == nil, nil
}

// mineFilter scope=mine 的口径：本人锁定或被分派给本人
func mineFilter(args *filters.Args, me *auth.UserRow) string {
	ph := args.Add(me.ID)
	return fmt.Sprintf("(o.claimed_by_id::text = %s OR o.assigned_to_id::text = %s)", ph, ph)
}

// poolWhere 待分配/可调派池的谓词。计数端点与列表端点共用同一份，
// 免得哪天改了一处忘了另一处——那种不一致的表现是「计数说 8336、
// 点进去只有 20 条」，而且很难第一时间怀疑到是两份谓词不同步。
func poolWhere(scope, mine string) func(*filters.Args, *auth.UserRow, bool) []string {
	return func(args *filters.Args, me *auth.UserRow, canAll bool) []string {
		where := []string{"o.status IN ('pooled','dispatching')"}
		switch {
		case scope == "free":
			where = append(where, "o.claimed_by_id IS NULL AND o.assigned_to_id IS NULL")
		case scope == "all" && canAll:
			// 超管/全局数据范围：看全量可调派池
		case scope == "mine" || scope == "all" || mine == "1" || mine == "true":
			where = append(where, mineFilter(args, me))
		}
		return where
	}
}

// dispatchedWhere 已调派池的谓词
func dispatchedWhere(scope, mine string) func(*filters.Args, *auth.UserRow, bool) []string {
	return func(args *filters.Args, me *auth.UserRow, canAll bool) []string {
		where := []string{"o.status = 'converted'"}
		switch {
		case scope == "all" && canAll:
		case scope == "mine" || scope == "all" || mine == "1" || mine == "true":
			where = append(where, mineFilter(args, me))
		}
		return where
	}
}

// PoolList GET /api/v1/orders/pool
func (h *Handler) PoolList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	h.poolPage(w, r, poolWhere(q.Get("scope"), q.Get("mine")),
		"ORDER BY o.priority DESC, o.pooled_at, o.id")
}

// Dispatched GET /api/v1/orders/dispatched
func (h *Handler) Dispatched(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	h.poolPage(w, r, dispatchedWhere(q.Get("scope"), q.Get("mine")),
		"ORDER BY o.created_at DESC, o.id")
}

// Dispatchers GET /api/v1/orders/dispatchers
func (h *Handler) Dispatchers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	isChief, _ := h.isChiefDispatcher(ctx, me)
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, COALESCE(NULLIF(nickname,''), username) AS name, username
		FROM accounts_user WHERE is_active ORDER BY nickname, username LIMIT 200`)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id, name, username string
		if rows.Scan(&id, &name, &username) != nil {
			break
		}
		list = append(list, map[string]any{"id": id, "name": name, "username": username})
	}
	meName := me.Nickname
	if meName == "" {
		meName = me.Username
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"is_chief":    isChief,
		"me":          map[string]any{"id": me.ID, "name": meName},
		"dispatchers": list,
	})
}

// CustomerAddresses GET /api/v1/orders/customer-addresses?customer=
//
// 取该客户最近 200 个历史站点，按 (类型, 城市, 地址) 去重后各留 10 条。
// 缺 customer 参数时回空，不报错——录单页在选客户前就会先打这个接口。
func (h *Handler) CustomerAddresses(w http.ResponseWriter, r *http.Request) {
	cid := r.URL.Query().Get("customer")
	empty := map[string]any{"pickup": []any{}, "delivery": []any{}}
	if cid == "" {
		httpx.JSON(w, http.StatusOK, empty)
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT s.stop_type, s.city, s.address, s.contact_name, s.contact_phone
		FROM ops_order_stop s JOIN ops_order o ON o.id = s.order_id
		WHERE o.customer_id = $1::uuid
		ORDER BY s.created_at DESC, s.id LIMIT 200`, cid)
	if err != nil {
		httpx.JSON(w, http.StatusOK, empty)
		return
	}
	defer rows.Close()
	seen := map[string]bool{}
	pickup, delivery := []map[string]any{}, []map[string]any{}
	for rows.Next() {
		var typ, city, addr, name, phone string
		if rows.Scan(&typ, &city, &addr, &name, &phone) != nil {
			break
		}
		key := typ + "\x00" + city + "\x00" + addr
		if seen[key] || (addr == "" && city == "") {
			continue
		}
		seen[key] = true
		item := map[string]any{"city": city, "address": addr, "contact_name": name, "contact_phone": phone}
		if typ == "pickup" {
			pickup = append(pickup, item)
		} else {
			delivery = append(delivery, item)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"pickup": headN(pickup, 10), "delivery": headN(delivery, 10),
	})
}

func headN(xs []map[string]any, n int) []map[string]any {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// Export GET /api/v1/orders/export —— 当前筛选结果导出 CSV（带 BOM 供 Excel 识别中文）
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	args := &filters.Args{}
	where := []string{"NOT o.is_deleted"}
	sw, err := h.scopeWhere(r, me, args)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if sw != "" {
		where = append(where, sw)
	}
	q := r.URL.Query()
	if s := strings.TrimSpace(q.Get("search")); s != "" {
		ph := args.Add("%" + s + "%")
		parts := make([]string, len(searchCols))
		for i, c := range searchCols {
			parts[i] = fmt.Sprintf("%s ILIKE %s", c, ph)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	for _, f := range []string{"status", "channel", "source_type", "business_type", "priority"} {
		if v := q.Get(f); v != "" {
			where = append(where, fmt.Sprintf("o.%s = %s", f, args.Add(v)))
		}
	}
	if frag := filters.Apply(q.Get("filter"), filterFields, args); frag != "" {
		where = append(where, frag)
	}
	rows, err := h.DB.Query(ctx, `
		SELECT o.order_no, COALESCE(c.name,''),
		       (CASE o.channel WHEN 'cs' THEN '客服代下' WHEN 'self' THEN '客户自助'
		            WHEN 'miniprogram' THEN '小程序' WHEN 'wechat_group' THEN '微信群'
		            WHEN 'api' THEN '开放API' ELSE o.channel END),
		       (CASE o.status WHEN 'draft' THEN '草稿' WHEN 'pending_confirm' THEN '待确认'
		            WHEN 'confirmed' THEN '已确认' WHEN 'pooled' THEN '订单池' WHEN 'dispatching' THEN '调度中'
		            WHEN 'converted' THEN '已派单' WHEN 'completed' THEN '已完成' WHEN 'cancelled' THEN '已取消'
		            ELSE o.status END),
		       o.origin, o.destination, o.cargo_weight_ton::text, o.cargo_quantity::text,
		       o.quoted_amount::text,
		       to_char(o.created_at AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD HH24:MI')
		`+fromClause+" WHERE "+strings.Join(where, " AND ")+" ORDER BY o.created_at DESC LIMIT 5000", args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败："+err.Error())
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8-sig")
	w.Header().Set("Content-Disposition", `attachment; filename="orders.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"订单号", "客户", "渠道", "状态", "始发", "目的", "货量(吨)", "件数", "报价", "创建时间"})
	for rows.Next() {
		rec := make([]string, 10)
		ptrs := make([]any, 10)
		for i := range rec {
			ptrs[i] = &rec[i]
		}
		if rows.Scan(ptrs...) != nil {
			break
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
}

// PoolCounts GET /api/v1/orders/pool-counts
//
// 调度工作台顶部那排计数（待派 / 紧急 / 临期超时）和三个池的角标。
//
// 为什么要单独一个端点：这些数以前是前端拿 items.length 数出来的。
// 演示库十几单时一页装得下全部，数出来正好是对的；真实数据量下
// 一页只有 20 条，于是「待分配 20」其实是 8336、「紧急 40」其实是两千多。
// 这类错误不会报错、不会变形，只是安静地把全量说成了一页——
// 而调度员正是照着这几个数决定今天先派哪一批。
//
// 计数走的谓词与列表端点是同一份（poolWhere / dispatchedWhere / urgentSQL），
// 所以「计数说有多少」和「点进去能翻到多少」不会各说各话。
func (h *Handler) PoolCounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	canAll, err := h.canViewAll(r, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	scope, mine := q.Get("scope"), q.Get("mine")

	// count 用主表 + 建单人关联即可：数据范围看 cb.organization_id，
	// 其余关联对计数没有影响，不必拖进来。
	count := func(build func(*filters.Args, *auth.UserRow, bool) []string, extra string) (int, error) {
		args := &filters.Args{}
		where := []string{"NOT o.is_deleted"}
		sw, err := h.scopeWhere(r, me, args)
		if err != nil {
			return 0, err
		}
		if sw != "" {
			where = append(where, sw)
		}
		where = append(where, build(args, me, canAll)...)
		if extra != "" {
			where = append(where, extra)
		}
		var n int
		err = h.DB.QueryRow(ctx, `SELECT count(*) FROM ops_order o
			LEFT JOIN accounts_user cb ON cb.id = o.created_by_id
			WHERE `+strings.Join(where, " AND "), args.Values...).Scan(&n)
		return n, err
	}

	free := poolWhere("free", "")
	minePool := poolWhere(scope, mine)
	disp := dispatchedWhere(scope, mine)

	// 待派/紧急/临期这三个数**不能**用「待分配 + 可调派」相加。
	//
	// 两池对普通调度是不相交的（未认领 vs 本人已认领），加起来没问题；
	// 但超管选「全部」时可调派池就是整个订单池，把待分配池整个包含在内，
	// 相加等于每张单数两遍——8336 + 8336 = 16672，而池里一共只有 8336 张。
	// 前端原来的 freeOrders.length + mineOrders.length 就是这么写的，
	// 演示库里 12 + 12 = 24 也一样是错的，只是没人会去核对那个数。
	//
	// 所以这里按「我能看见的整个待派池」一次数完，谓词取两池的并集。
	visible := func(args *filters.Args, me *auth.UserRow, canAll bool) []string {
		where := []string{"o.status IN ('pooled','dispatching')"}
		if scope == "all" && canAll {
			return where // 全量可见：并集就是整池
		}
		// 未认领未分派（人人可见） OR 本人认领/被分派
		where = append(where, "((o.claimed_by_id IS NULL AND o.assigned_to_id IS NULL) OR "+
			mineFilter(args, me)+")")
		return where
	}

	type job struct {
		key   string
		build func(*filters.Args, *auth.UserRow, bool) []string
		extra string
	}
	out := map[string]int{}
	for _, j := range []job{
		{"unassigned", free, ""},
		{"dispatchable", minePool, ""},
		{"dispatched", disp, ""},
		{"pending", visible, ""},
		{"urgent", visible, urgentSQL},
		{"at_risk", visible, "o.sla_status IN ('at_risk','breached')"},
	} {
		n, err := count(j.build, j.extra)
		if err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "统计失败")
			return
		}
		out[j.key] = n
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"unassigned":   out["unassigned"],
		"dispatchable": out["dispatchable"],
		"dispatched":   out["dispatched"],
		"pending":      out["pending"],
		"urgent":       out["urgent"],
		"at_risk":      out["at_risk"],
	})
}
