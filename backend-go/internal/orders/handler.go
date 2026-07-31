// Package orders 订单域（参考实现）：/orders 列表的 Go 原生移植。
//
// 契约对齐 Django OrderViewSet：
//   - 分页信封 {items,total,page,page_size,pages}（page/page_size 参数，上限 200）
//   - search= 多列 ILIKE（order_no/remark/contact_phone/origin/destination/customer.name）
//   - ordering= 白名单排序（-前缀=降序），customer__name 等跨表字段映射 JOIN 列
//   - filter=<JSON> FilterBuilder 模型（internal/filters）
//   - 数据范围：superuser/all 全量，org/org_sub 按建单人组织子树（iam.scoping 语义）
//   - 行内计算字段 dispatchable/lock_state/exception_* 与序列化器逻辑一致
//
// 该文件是后续所有资源移植的模板：一条主 SQL（JOIN + LATERAL 聚合）替代
// Django 的 select_related/prefetch_related，天然无 N+1。
package orders

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// ProjectResolver 建单时的项目「取或建」（由 finance 域实现）
type ProjectResolver interface {
	EnsureProject(ctx context.Context, name, customerID string) (string, string, error)
}

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
	// Projects 建单表单里直接新建项目时用；为 nil 时该能力静默关闭
	Projects ProjectResolver
}

// ordering= 白名单：前端 sortField → SQL 列（防注入 + 契约文档化）
var orderingCols = map[string]string{
	"created_at":       "o.created_at",
	"order_no":         "o.order_no",
	"priority":         "o.priority",
	"status":           "o.status",
	"sla_status":       "o.sla_status",
	"channel":          "o.channel",
	"business_type":    "o.business_type",
	"quoted_amount":    "o.quoted_amount",
	"cargo_weight_ton": "o.cargo_weight_ton",
	"customer__name":   "c.name",
}

// filter=<JSON> 字段映射，对齐 OrderViewSet.server_filter_fields
var filterFields = map[string]filters.FilterField{
	"order_no":      {Type: filters.Text, Cols: []string{"o.order_no"}},
	"customer":      {Type: filters.Text, Cols: []string{"c.name"}},
	"route":         {Type: filters.Text, Cols: []string{"o.origin", "o.destination"}},
	"creator":       {Type: filters.Text, Cols: []string{"cb.username", "cb.nickname"}},
	"status":        {Type: filters.Enum, Cols: []string{"o.status"}},
	"channel":       {Type: filters.Enum, Cols: []string{"o.channel"}},
	"business_type": {Type: filters.Enum, Cols: []string{"o.business_type"}},
	"priority":      {Type: filters.Enum, Cols: []string{"o.priority"}},
	"settlement":    {Type: filters.Enum, Cols: []string{"o.settlement_type"}},
	"level":         {Type: filters.Enum, Cols: []string{"c.level"}},
	"sla":           {Type: filters.Enum, Cols: []string{"o.sla_status"}},
	"exception":     {Type: filters.Enum, Cols: []string{"exc.has_open::text"}},
	"amount":        {Type: filters.Number, Cols: []string{"o.quoted_amount"}},
	"weight":        {Type: filters.Number, Cols: []string{"o.cargo_weight_ton"}},
	"created_at":    {Type: filters.Date, Cols: []string{"o.created_at"}},
}

var searchCols = []string{"o.order_no", "o.remark", "o.contact_phone", "o.origin", "o.destination", "c.name"}

var freightTermLabel = map[string]string{"prepaid": "现付", "collect": "到付", "receipt": "回单付", "monthly": "月结"}
var freightPayerLabel = map[string]string{"shipper": "发货方", "consignee": "收货方", "third_party": "第三方"}

// fromClause 主查询的 JOIN 面：LATERAL 聚合替代 prefetch，一次往返出全部嵌套。
const fromClause = `
FROM ops_order o
LEFT JOIN md_customer c ON c.id = o.customer_id
LEFT JOIN accounts_user cb ON cb.id = o.created_by_id
LEFT JOIN fin_project pj ON pj.id = o.project_id
LEFT JOIN accounts_user clb ON clb.id = o.claimed_by_id
LEFT JOIN accounts_user asb ON asb.id = o.assigned_to_id
LEFT JOIN accounts_user abb ON abb.id = o.assigned_by_id
LEFT JOIN LATERAL (
  SELECT count(*) FILTER (WHERE e.status NOT IN ('closed','rejected')) AS open_count,
         (count(*) FILTER (WHERE e.status NOT IN ('closed','rejected'))) > 0 AS has_open,
         COALESCE(max(CASE e.level WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END)
                  FILTER (WHERE e.status NOT IN ('closed','rejected')), 0) AS max_level
  FROM ops_exception e WHERE e.order_id = o.id
) exc ON true
LEFT JOIN LATERAL (
  SELECT COALESCE(array_agg(w.waybill_no ORDER BY w.created_at), '{}') AS nos,
         min(w.created_at) AS first_dispatched
  FROM ops_waybill w WHERE w.order_id = o.id
) wb ON true`

func clampInt(s string, def, lo, hi int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

// List GET /api/v1/orders
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	page := clampInt(q.Get("page"), 1, 1, 1<<30)
	pageSize := clampInt(q.Get("page_size"), 20, 1, 200)

	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}

	args := &filters.Args{}
	where := []string{"NOT o.is_deleted"}

	// 数据范围：按建单人组织子树（org_field=created_by__organization）
	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			where = append(where, "false")
		} else {
			where = append(where, fmt.Sprintf("cb.organization_id::text = ANY(%s)", args.Add(scopeIDs)))
		}
	}

	// search= 跨列 ILIKE
	if s := strings.TrimSpace(q.Get("search")); s != "" {
		ph := args.Add("%" + s + "%")
		parts := make([]string, len(searchCols))
		for i, c := range searchCols {
			parts[i] = fmt.Sprintf("%s ILIKE %s", c, ph)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}

	// filterset_fields 直连参数（与 DRF 平级支持）
	for _, f := range []string{"status", "channel", "source_type", "business_type", "priority"} {
		if v := q.Get(f); v != "" {
			where = append(where, fmt.Sprintf("o.%s = %s", f, args.Add(v)))
		}
	}

	// filter=<JSON> FilterBuilder 模型
	if frag := filters.Apply(q.Get("filter"), filterFields, args); frag != "" {
		where = append(where, frag)
	}

	whereSQL := "WHERE " + strings.Join(where, " AND ")

	// ordering= 白名单（多字段逗号分隔，- 前缀降序），默认 -created_at
	orderSQL := "ORDER BY o.created_at DESC"
	if raw := q.Get("ordering"); raw != "" {
		var parts []string
		for _, f := range strings.Split(raw, ",") {
			f = strings.TrimSpace(f)
			desc := strings.HasPrefix(f, "-")
			col, ok := orderingCols[strings.TrimPrefix(f, "-")]
			if !ok {
				continue
			}
			dir := "ASC"
			if desc {
				dir = "DESC"
			}
			parts = append(parts, col+" "+dir)
		}
		if len(parts) > 0 {
			orderSQL = "ORDER BY " + strings.Join(parts, ", ") + ", o.id"
		}
	}

	// 总数（同 WHERE，不带排序分页）
	var total int
	if err := h.DB.QueryRow(ctx, "SELECT count(*) "+fromClause+" "+whereSQL, args.Values...).Scan(&total); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}

	limitPh := args.Add(pageSize)
	offsetPh := args.Add((page - 1) * pageSize)
	rows, err := h.DB.Query(ctx, selectOrderSQL+fromClause+" "+whereSQL+" "+orderSQL+
		fmt.Sprintf(" LIMIT %s OFFSET %s", limitPh, offsetPh), args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()

	isChief, _ := h.isChiefDispatcher(ctx, me)
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

// Funnel GET /api/v1/orders/funnel —— 建单工作台管道计数
func (h *Handler) Funnel(w http.ResponseWriter, r *http.Request) {
	// 原先这条不带数据范围：一个什么权限都没有的账号能拿到全库订单漏斗
	// （总量、分状态、分渠道、今日建单）。/orders 列表是按建单人组织收窄的，
	// 同一批数据换个聚合口径就全量放出去，等于列表那道收窄白做。
	// 口径必须和列表一致——用 cb.organization_id（建单人所属组织），不是 o.organization_id。
	actor := h.Svc.Guard(w, r, "", "")
	if actor == nil {
		return
	}
	ctx := r.Context()
	args := &filters.Args{}
	scope := actor.ScopeSQL("cb.organization_id::text", args)
	byStatus, byChannel := map[string]int{}, map[string]int{}
	rows, err := h.DB.Query(ctx, `
		SELECT o.status, o.channel, count(*)
		FROM ops_order o LEFT JOIN accounts_user cb ON cb.id = o.created_by_id
		WHERE NOT o.is_deleted AND `+scope+`
		GROUP BY o.status, o.channel`, args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var st, ch string
		var n int
		if err := rows.Scan(&st, &ch, &n); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
			return
		}
		byStatus[st] += n
		byChannel[ch] += n
		total += n
	}
	var today int
	targs := &filters.Args{}
	tscope := actor.ScopeSQL("cb.organization_id::text", targs)
	_ = h.DB.QueryRow(ctx, `
		SELECT count(*) FROM ops_order o LEFT JOIN accounts_user cb ON cb.id = o.created_by_id
		WHERE NOT o.is_deleted AND `+tscope+`
		  AND (o.created_at AT TIME ZONE 'Asia/Shanghai')::date = (now() AT TIME ZONE 'Asia/Shanghai')::date`,
		targs.Values...).Scan(&today)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"by_status": byStatus, "by_channel": byChannel, "today_created": today, "total": total,
	})
}

func (h *Handler) isChiefDispatcher(ctx context.Context, u *auth.UserRow) (bool, error) {
	if u.IsSuperuser {
		return true, nil
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, u)
	if err != nil {
		return false, err
	}
	for _, p := range perms {
		if p == "*" || p == "dispatch.assign" {
			return true, nil
		}
	}
	return false, nil
}

// selectOrderSQL 列表/单条回读共用的序列化列面（与 OrderSerializer 逐字段对齐）
const selectOrderSQL = `
SELECT o.id::text, o.order_no, o.customer_id::text, COALESCE(c.name,''), COALESCE(c.level,''),
       o.channel, o.source, o.source_type, o.business_type, o.priority, o.settlement_type, o.status,
       o.freight_term, o.freight_payer, o.cod_amount::text, o.cod_status,
       o.contact_name, o.contact_phone, o.origin, o.destination,
       o.pickup_address, o.pickup_contact_name, o.pickup_contact_phone,
       o.delivery_address, o.delivery_contact_name, o.delivery_contact_phone,
       o.cargo_desc, o.cargo_quantity, o.cargo_weight_ton::text, o.cargo_volume_cbm::text,
       o.cargo_value::text, o.package_type, o.is_hazardous, o.temperature_range, o.quoted_amount::text,
       o.expected_pickup_at, o.expected_delivery_at, o.sla_status, o.delivered_at,
       o.claimed_by_id::text, COALESCE(NULLIF(clb.nickname,''), clb.username, ''), o.claimed_at, o.pooled_at,
       o.assigned_to_id::text, COALESCE(NULLIF(asb.nickname,''), asb.username, ''),
       COALESCE(abb.username,''), o.assigned_at,
       o.created_by_id::text, COALESCE(cb.username,''), o.raw_text, o.ai_conversation_id,
       COALESCE(o.parse_meta,'{}'::jsonb), o.remark, o.created_at,
       o.project_id::text, COALESCE(pj.name,''),
       wb.nos, wb.first_dispatched,
       exc.open_count, exc.max_level,
       o.approval_status, o.approval_remark, o.approved_at,
       COALESCE((SELECT json_agg(json_build_object(
           'id', ci.id::text, 'seq', ci.seq, 'name', ci.name, 'quantity', ci.quantity,
           'weight_ton', ci.weight_ton::text, 'volume_cbm', ci.volume_cbm::text,
           'package_type', ci.package_type, 'temperature_range', ci.temperature_range, 'remark', ci.remark
         ) ORDER BY ci.seq) FROM ops_order_cargo_item ci WHERE ci.order_id = o.id), '[]'::json),
       COALESCE((SELECT json_agg(json_build_object(
           'id', st.id::text, 'seq', st.seq, 'stop_type', st.stop_type, 'city', st.city, 'address', st.address,
           'contact_name', st.contact_name, 'contact_phone', st.contact_phone,
           'expected_start', st.expected_start, 'expected_end', st.expected_end, 'cargo_note', st.cargo_note
         ) ORDER BY st.seq) FROM ops_order_stop st WHERE st.order_id = o.id), '[]'::json)
`
