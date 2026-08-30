// Package waybills 运单域读路径：GET /waybills 列表 + /waybills/stats。
// 契约对齐 Django WaybillViewSet + WaybillSerializer（逐字段），模式复用 orders 模板。
package waybills

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/filters"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

type Handler struct {
	DB  *pgxpool.Pool
	Svc *auth.Service
}

// allow 权限闸：这个动作要不要这个权限点。
//
// 由来是发布前的一次系统性排查：拿一个只有 masterdata.view + waybill.view 的
// 客服账号，把 100 条动作路由挨个打了一遍，**53 条没有被 403 挡住**。
// 挡住其中一部分的只是数据范围——而数据范围管的是"看得见谁的单"，
// 不是"能不能做这件事"：同一个网点的客服照样能派单、能签收、能核销。
//
// 这个包里本来就有带权限的入口（resolve(w, r, "waybill.manage")），
// 只是那些自己取参数、不走 resolve 的 handler 全都漏了。
// 现在缺的那些补上，并逐条登记进 cmd/server/authz_test.go 的清单。
func (h *Handler) allow(w http.ResponseWriter, r *http.Request, want string) bool {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return false
	}
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取权限失败")
		return false
	}
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "缺少所需权限。")
	return false
}

var dispatchTypeLabel = map[string]string{
	"own_vehicle": "自营单车", "fleet": "自营车队", "third_party": "外包承运商", "platform": "网货平台",
}
var channelLabel = map[string]string{
	"own_vehicle": "自营", "fleet": "自营", "third_party": "外包", "platform": "网货",
}
var codStatusLabel = map[string]string{"none": "无代收", "pending": "待代收", "collected": "已代收", "remitted": "已回款"}
var freightTermLabel = map[string]string{"prepaid": "现付", "collect": "到付", "receipt": "回单付", "monthly": "月结"}
var freightPayerLabel = map[string]string{"shipper": "发货方", "consignee": "收货方", "third_party": "第三方"}
var employmentLabel = map[string]string{"employee": "自有员工", "outsourced": "外协外调", "carrier_driver": "承运商司机", "temp": "临时"}
var roleLabel = map[string]string{"main": "主驾", "co": "副驾", "relay": "接力"}

var orderingCols = map[string]string{
	"eta_drift_minutes": "w.eta_drift_minutes",
	"created_at":        "w.created_at",
	"waybill_no":        "w.waybill_no",
	"customer__name":    "c.name",
	"status":            "w.status",
	"receipt_status":    "w.receipt_status",
	"receivable_total":  "fin.receivable_total",
	"payable_total":     "fin.payable_total",
	"cod_amount":        "w.cod_amount",
}

var filterFields = map[string]filters.FilterField{
	"waybill_no": {Type: filters.Text, Cols: []string{"w.waybill_no"}},
	"customer":   {Type: filters.Text, Cols: []string{"c.name"}},
	"route":      {Type: filters.Text, Cols: []string{"w.origin", "w.destination"}},
	"vehicle":    {Type: filters.Text, Cols: []string{"v.plate_no"}},
	"status":     {Type: filters.Enum, Cols: []string{"w.status"}},
	"channel": {Type: filters.Enum, Cols: []string{
		"(CASE WHEN w.dispatch_type IN ('own_vehicle','fleet') THEN '自营' WHEN w.dispatch_type='third_party' THEN '外包' WHEN w.dispatch_type='platform' THEN '网货' ELSE '' END)"}},
	"receipt":    {Type: filters.Enum, Cols: []string{"w.receipt_status"}},
	"receivable": {Type: filters.Number, Cols: []string{"fin.receivable_total"}},
	"payable":    {Type: filters.Number, Cols: []string{"fin.payable_total"}},
	"cod":        {Type: filters.Number, Cols: []string{"w.cod_amount"}},
}

var searchCols = []string{"w.waybill_no", "w.route_name", "c.name", "v.plate_no", "w.origin", "w.destination"}

const fromClause = `
FROM ops_waybill w
LEFT JOIN md_customer c ON c.id = w.customer_id
LEFT JOIN md_carrier ca ON ca.id = w.carrier_id
LEFT JOIN md_vehicle v ON v.id = w.vehicle_id
LEFT JOIN md_vehicle tr ON tr.id = w.trailer_id
LEFT JOIN md_driver d ON d.id = w.driver_id
LEFT JOIN ops_dispatch_batch b ON b.id = w.batch_id
LEFT JOIN LATERAL (
  SELECT COALESCE(sum(e.amount) FILTER (WHERE e.direction='receivable'), 0) AS receivable_total,
         COALESCE(sum(e.amount) FILTER (WHERE e.direction='payable'), 0) AS payable_total
  FROM fin_expense_record e WHERE e.waybill_id = w.id
) fin ON true`

func hasPerm(perms []string, want string) bool {
	for _, p := range perms {
		if p == "*" || p == want {
			return true
		}
	}
	return false
}

func clampInt(s string, def, lo, hi int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

// List GET /api/v1/waybills
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
	_, _, perms, err := h.Svc.RolesAndPerms(ctx, me)
	if err != nil || !hasPerm(perms, "waybill.view") {
		httpx.Err(w, http.StatusForbidden, "PERMISSION_DENIED", "无运单查看权限")
		return
	}

	args := &filters.Args{}
	where := []string{"true"}

	scopeIDs, err := h.Svc.ScopeOrgIDs(ctx, me)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取数据范围失败")
		return
	}
	if scopeIDs != nil {
		if len(scopeIDs) == 0 {
			where = append(where, "false")
		} else {
			where = append(where, fmt.Sprintf("w.organization_id::text = ANY(%s)", args.Add(scopeIDs)))
		}
	}

	if s := strings.TrimSpace(q.Get("search")); s != "" {
		ph := args.Add("%" + s + "%")
		parts := make([]string, len(searchCols))
		for i, c := range searchCols {
			parts[i] = fmt.Sprintf("%s ILIKE %s", c, ph)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	for _, f := range []string{"status", "risk_level", "receipt_status"} {
		if v := q.Get(f); v != "" {
			where = append(where, fmt.Sprintf("w.%s = %s", f, args.Add(v)))
		}
	}
	if frag := filters.Apply(q.Get("filter"), filterFields, args); frag != "" {
		where = append(where, frag)
	}
	whereSQL := "WHERE " + strings.Join(where, " AND ")

	// 默认排序对齐 DRF ordering = ["-eta_drift_minutes","risk_level","waybill_no"]
	orderSQL := "ORDER BY w.eta_drift_minutes DESC, w.risk_level ASC, w.waybill_no ASC"
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
			orderSQL = "ORDER BY " + strings.Join(parts, ", ") + ", w.id"
		}
	}

	var total int
	if err := h.DB.QueryRow(ctx, "SELECT count(*) "+fromClause+" "+whereSQL, args.Values...).Scan(&total); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}

	limitPh := args.Add(pageSize)
	offsetPh := args.Add((page - 1) * pageSize)
	rows, err := h.DB.Query(ctx, selectWaybillSQL+fromClause+" "+whereSQL+" "+orderSQL+
		fmt.Sprintf(" LIMIT %s OFFSET %s", limitPh, offsetPh), args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0, pageSize)
	for rows.Next() {
		it, err := scanWaybill(rows)
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

// Stats GET /api/v1/waybills/stats —— 状态药丸计数
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	// 这条原先既没有权限闸也没有数据范围：任何登录账号都能拿到全库运单的
	// 状态分布与总数。是 cmd/server/authz_test.go 第一次跑就抓出来的——
	// 和财务域同一类漏（手写 handler 忘了挂闸），只是当初手工探针没打到这条。
	actor := h.Svc.Guard(w, r, "waybill.view", "无运单查看权限")
	if actor == nil {
		return
	}
	ctx := r.Context()
	byStatus := map[string]int{}
	total := 0
	args := &filters.Args{}
	scope := actor.ScopeSQL("w.organization_id::text", args)
	rows, err := h.DB.Query(ctx,
		"SELECT w.status, count(*) FROM ops_waybill w WHERE "+scope+" GROUP BY w.status", args.Values...)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "查询失败")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取失败")
			return
		}
		byStatus[st] = n
		total += n
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"by_status": byStatus, "total": total})
}

type driverRow struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Phone         string `json:"phone"`
	Wechat        string `json:"wechat"`
	AppRegistered bool   `json:"app_registered"`
	Role          string `json:"role"`
	Employment    string `json:"employment"`
	Note          string `json:"note"`
}

// selectWaybillSQL 列表/详情共用的序列化列面（与 WaybillSerializer 逐字段对齐）
const selectWaybillSQL = `
SELECT w.id::text, w.waybill_no, COALESCE(b.batch_no,''), COALESCE(c.name,''), COALESCE(ca.name,''),
       COALESCE(v.plate_no,''), COALESCE(tr.plate_no,''), COALESCE(d.name,''), COALESCE(d.phone,''),
       COALESCE(d.employment_type,''),
       w.route_name, w.ai_conversation_id, w.origin, w.destination, w.status, w.dispatch_status, w.risk_level,
       w.dispatch_type, w.platform_name, w.platform_order_no,
       w.receipt_status, w.eta_drift_minutes, w.planned_arrival, w.estimated_arrival,
       w.loaded_at, w.departed_at, w.arrived_at, w.signed_at,
       w.freight_term, w.freight_payer,
       w.cod_amount::text, w.cod_status, w.cod_collected_at, w.cod_remitted_at,
       fin.receivable_total::float8, fin.payable_total::float8,
       w.cargo_quantity, w.cargo_weight_ton::float8, w.cargo_volume_cbm::float8, w.created_at,
       COALESCE((SELECT json_agg(json_build_object(
           'id', wd.driver_id::text, 'name', dd.name, 'phone', dd.phone,
           'wechat', dd.wechat, 'app_registered', dd.app_registered,
           'role', wd.role, 'employment', dd.employment_type, 'note', wd.note
         ) ORDER BY wd.created_at) FROM ops_waybill_driver wd JOIN md_driver dd ON dd.id = wd.driver_id
         WHERE wd.waybill_id = w.id), '[]'::json)
`

func scanWaybill(rows pgx.Rows) (map[string]any, error) {
	var (
		id, waybillNo, batchNo, customerName, carrierName   string
		vehiclePlate, trailerPlate, driverName, driverPhone string
		driverEmployment                                    string
		routeName, aiConvID, origin, destination            string
		status, dispatchStatus, riskLevel, dispatchType     string
		platformName, platformOrderNo, receiptStatus        string
		etaDrift                                            int
		plannedArrival, estimatedArrival                    *time.Time
		loadedAt, departedAt, arrivedAt, signedAt           *time.Time
		freightTerm, freightPayer, codStatus                string
		codAmount                                           *string
		codCollectedAt, codRemittedAt                       *time.Time
		receivable, payable, cargoWeight, cargoVolume       float64
		cargoQty                                            int
		createdAt                                           time.Time
		driversJSON                                         json.RawMessage
	)
	if err := rows.Scan(
		&id, &waybillNo, &batchNo, &customerName, &carrierName,
		&vehiclePlate, &trailerPlate, &driverName, &driverPhone,
		&driverEmployment,
		&routeName, &aiConvID, &origin, &destination, &status, &dispatchStatus, &riskLevel,
		&dispatchType, &platformName, &platformOrderNo,
		&receiptStatus, &etaDrift, &plannedArrival, &estimatedArrival,
		&loadedAt, &departedAt, &arrivedAt, &signedAt,
		&freightTerm, &freightPayer,
		&codAmount, &codStatus, &codCollectedAt, &codRemittedAt,
		&receivable, &payable,
		&cargoQty, &cargoWeight, &cargoVolume, &createdAt,
		&driversJSON,
	); err != nil {
		return nil, err
	}

	// drivers 数组补 role_label / employment 中文（与序列化器 get_drivers 输出一致）
	var drivers []map[string]any
	var raw []driverRow
	_ = json.Unmarshal(driversJSON, &raw)
	for _, d := range raw {
		drivers = append(drivers, map[string]any{
			"id": d.ID, "name": d.Name, "phone": d.Phone, "wechat": d.Wechat,
			"app_registered": d.AppRegistered, "role": d.Role, "role_label": roleLabel[d.Role],
			"employment": employmentLabel[d.Employment], "note": d.Note,
		})
	}
	if drivers == nil {
		drivers = []map[string]any{}
	}

	return map[string]any{
		"id": id, "waybill_no": waybillNo, "batch_no": batchNo,
		"customer_name": customerName, "carrier_name": carrierName,
		"vehicle_plate": vehiclePlate, "trailer_plate": trailerPlate,
		"driver_name": driverName, "driver_phone": driverPhone,
		"driver_employment": employmentLabel[driverEmployment],
		"drivers":           drivers,
		"route_name":        routeName, "ai_conversation_id": aiConvID,
		"origin": origin, "destination": destination,
		"status": status, "dispatch_status": dispatchStatus, "risk_level": riskLevel,
		"dispatch_type": dispatchType, "dispatch_type_label": dispatchTypeLabel[dispatchType],
		"channel":       channelLabel[dispatchType],
		"platform_name": platformName, "platform_order_no": platformOrderNo,
		"receipt_status": receiptStatus, "eta_drift_minutes": etaDrift,
		"planned_arrival": plannedArrival, "estimated_arrival": estimatedArrival,
		"loaded_at": loadedAt, "departed_at": departedAt, "arrived_at": arrivedAt, "signed_at": signedAt,
		"freight_term": freightTerm, "freight_term_label": freightTermLabel[freightTerm],
		"freight_payer": freightPayer, "freight_payer_label": freightPayerLabel[freightPayer],
		"cod_amount": codAmount, "cod_status": codStatus, "cod_status_label": codStatusLabel[codStatus],
		"cod_collected_at": codCollectedAt, "cod_remitted_at": codRemittedAt,
		"receivable_amount": receivable, "payable_amount": payable,
		"cargo":      map[string]any{"quantity": cargoQty, "weight_ton": cargoWeight, "volume_cbm": cargoVolume},
		"created_at": createdAt,
	}, nil
}
