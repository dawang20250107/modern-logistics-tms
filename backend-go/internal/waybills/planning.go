package waybills

// 智能调度建议（只出建议，不自动派车）：
//   GET  /waybills/{no}/dispatch-recommendation  车辆候选 + 司机候选 + 承运商比价
//   POST /waybills/dispatch-plan                 批量贪心排线
//
// 对齐 apps/ops/dispatch.{rank_vehicles, available_drivers, carrier_quotes,
// recommend_dispatch, plan_dispatch}。装载适配与比价是纯规则，可复算可解释。

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/finance"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// busyStatuses 已派车至在途的运单占用运力，不可再分配
var busyStatuses = []string{"dispatched", "loaded", "departed", "in_transit"}

type vehicleRow struct {
	ID, PlateNo, BodyType, VehicleClass string
	LengthM, CapTon, CapCbm             float64
	InspectionExpiry, InsuranceExpiry   *time.Time
	MaintenanceDue                      *time.Time
}

type cargoNeed struct {
	WeightTon, VolumeCbm float64
	NeedsReefer, Hazmat  bool
}

// availableVehicles 在用且未被在途运单占用的车
func (h *Handler) availableVehicles(ctx context.Context) ([]vehicleRow, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, plate_no, body_type, vehicle_class,
		       COALESCE(vehicle_length_m,0)::float8, COALESCE(load_capacity_ton,0)::float8,
		       COALESCE(volume_capacity_cbm,0)::float8,
		       inspection_expiry, insurance_expiry, maintenance_due_date
		FROM md_vehicle
		WHERE is_active AND NOT is_deleted
		  AND id NOT IN (SELECT vehicle_id FROM ops_waybill
		                 WHERE status = ANY($1) AND vehicle_id IS NOT NULL)
		ORDER BY plate_no, id`, busyStatuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []vehicleRow{}
	for rows.Next() {
		var v vehicleRow
		if rows.Scan(&v.ID, &v.PlateNo, &v.BodyType, &v.VehicleClass, &v.LengthM,
			&v.CapTon, &v.CapCbm, &v.InspectionExpiry, &v.InsuranceExpiry, &v.MaintenanceDue) != nil {
			break
		}
		out = append(out, v)
	}
	return out, nil
}

// complianceIssues 已过期的车辆证件标签；顺序对齐 Django 的检查顺序
func complianceIssues(v vehicleRow, today time.Time) []string {
	issues := []string{}
	for _, c := range []struct {
		exp   *time.Time
		label string
	}{{v.InspectionExpiry, "年检"}, {v.InsuranceExpiry, "保险"}, {v.MaintenanceDue, "维保"}} {
		if c.exp != nil && c.exp.Before(today) {
			issues = append(issues, c.label)
		}
	}
	return issues
}

// bodyTypeMismatch 车厢结构是否满足货物要求；空串表示匹配
func bodyTypeMismatch(v vehicleRow, need cargoNeed) string {
	if need.NeedsReefer && v.BodyType != "reefer" {
		return "冷链货需冷藏车"
	}
	if need.Hazmat && v.BodyType != "hazmat" && v.BodyType != "tank" {
		return "危险品需危运/罐式车"
	}
	return ""
}

type vehicleFit struct {
	m     map[string]any
	slack float64
	okCmp bool
}

// fitOf 车辆对运单的适配；核载/容积/车厢结构不满足返回 nil（硬性排除）
func fitOf(v vehicleRow, need cargoNeed, today time.Time) *vehicleFit {
	if v.CapTon != 0 && need.WeightTon > v.CapTon {
		return nil
	}
	if v.CapCbm != 0 && need.VolumeCbm > v.CapCbm {
		return nil
	}
	if bodyTypeMismatch(v, need) != "" {
		return nil // 不派敞车拉冷链、不派普通车拉危货——这条不留人工放行的口子
	}
	slackT, slackV := 1e9, 1e9
	if v.CapTon != 0 {
		slackT = v.CapTon - need.WeightTon
	}
	if v.CapCbm != 0 {
		slackV = v.CapCbm - need.VolumeCbm
	}
	slack := round2(slackT + slackV)
	util := 0.0
	if v.CapTon != 0 {
		util = round3(need.WeightTon / v.CapTon)
	}
	cmp := complianceIssues(v, today)
	blocked := len(cmp) > 0 // DISPATCH_BLOCK_ON_EXPIRED 默认 True
	reason := ""
	if blocked {
		reason = "证件过期屏蔽：" + joinSlash(cmp)
	}
	return &vehicleFit{
		m: map[string]any{
			"plate_no": v.PlateNo, "body_type": v.BodyType,
			"vehicle_length_m": v.LengthM, "slack": slack, "utilization": util,
			"compliance": cmp, "compliance_ok": len(cmp) == 0,
			"blocked": blocked, "block_reason": reason,
			"vehicle_id": v.ID,
		},
		slack: slack, okCmp: len(cmp) == 0,
	}
}

func joinSlash(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "/"
		}
		out += x
	}
	return out
}

// round2/round3 复刻 Python round(float, n)：十进制半偶入。
// math.Round 是半远入，装载率 0.3125 会被舍成 0.313 而 Python 给 0.312，
// 排序并列时这一位之差就足以让候选车次序不同。
func round2(f float64) float64 { return pyRound(f, 2) }
func round3(f float64) float64 { return pyRound(f, 3) }

func pyRound(v float64, n int) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', n, 64), 64)
	return f
}

// rankVehicles 合规车优先，其次紧凑装载优先（slack 越小越优）
func rankVehicles(vehicles []vehicleRow, need cargoNeed, today time.Time, includeBlocked bool) []map[string]any {
	fits := []*vehicleFit{}
	for _, v := range vehicles {
		f := fitOf(v, need, today)
		if f == nil {
			continue
		}
		if f.m["blocked"].(bool) && !includeBlocked {
			continue
		}
		fits = append(fits, f)
	}
	sort.SliceStable(fits, func(i, j int) bool {
		if fits[i].okCmp != fits[j].okCmp {
			return fits[i].okCmp // 合规的排前
		}
		return fits[i].slack < fits[j].slack
	})
	out := make([]map[string]any, 0, len(fits))
	for _, f := range fits {
		out = append(out, f.m)
	}
	return out
}

// RankVehiclesFor 供 orders 域复用：按货量与车厢要求给可派车辆排名。
// top<=0 表示不截断。
func RankVehiclesFor(ctx context.Context, h *Handler, weightTon, volumeCbm float64,
	needsReefer, hazmat bool, top int) ([]map[string]any, error) {
	vehicles, err := h.availableVehicles(ctx)
	if err != nil {
		return nil, err
	}
	out := rankVehicles(vehicles,
		cargoNeed{WeightTon: weightTon, VolumeCbm: volumeCbm, NeedsReefer: needsReefer, Hazmat: hazmat},
		cstMidnight(), false)
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out, nil
}

// RankVehiclesForExcluding 排除已占用车辆后再排名（拼单配载逐趟取车用）
func RankVehiclesForExcluding(ctx context.Context, h *Handler, weightTon, volumeCbm float64,
	needsReefer, hazmat bool, used map[string]bool) ([]map[string]any, error) {
	vehicles, err := h.availableVehicles(ctx)
	if err != nil {
		return nil, err
	}
	free := make([]vehicleRow, 0, len(vehicles))
	for _, v := range vehicles {
		if !used[v.ID] {
			free = append(free, v)
		}
	}
	return rankVehicles(free,
		cargoNeed{WeightTon: weightTon, VolumeCbm: volumeCbm, NeedsReefer: needsReefer, Hazmat: hazmat},
		cstMidnight(), false), nil
}

// CarrierQuotesFor 供 orders 域复用：多承运商比价
func CarrierQuotesFor(ctx context.Context, h *Handler, weightTon decimal.Decimal) ([]map[string]any, error) {
	return h.carrierQuotes(ctx, weightTon)
}

// needOf 从运单推导对车辆的硬性要求（冷链/危险品要素挂在订单上）
func (h *Handler) needOf(ctx context.Context, waybillID string) (cargoNeed, error) {
	var n cargoNeed
	var temp string
	var biz string
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(w.cargo_weight_ton,0)::float8, COALESCE(w.cargo_volume_cbm,0)::float8,
		       COALESCE(o.is_hazardous,false), COALESCE(o.temperature_range,''), COALESCE(o.business_type,'')
		FROM ops_waybill w LEFT JOIN ops_order o ON o.id = w.order_id
		WHERE w.id = $1::uuid`, waybillID).
		Scan(&n.WeightTon, &n.VolumeCbm, &n.Hazmat, &temp, &biz)
	n.NeedsReefer = temp != "" || biz == "coldchain"
	return n, err
}

// carrierQuotes 多承运商比价：每家取最低适用成本价，价低者在前。
// 黑名单承运商不进比价——推荐了又在派单时被硬阻断，等于白推。
func (h *Handler) carrierQuotes(ctx context.Context, weightTon decimal.Decimal) ([]map[string]any, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, name FROM md_carrier
		WHERE is_active AND NOT blacklisted AND NOT is_deleted ORDER BY code, id`)
	if err != nil {
		return nil, err
	}
	type c struct{ id, name string }
	carriers := []c{}
	for rows.Next() {
		var x c
		if rows.Scan(&x.id, &x.name) != nil {
			break
		}
		carriers = append(carriers, x)
	}
	rows.Close()

	out := []map[string]any{}
	for _, ca := range carriers {
		rules, err := finance.CostRulesFor(ctx, h.DB, ca.id, cstMidnight())
		if err != nil || len(rules) == 0 {
			continue
		}
		var best *decimal.Decimal
		for _, rule := range rules {
			amt := finance.Quote(rule, finance.QuoteInput{WeightTon: weightTon}).Amount
			if best == nil || amt.LessThan(*best) {
				v := amt
				best = &v
			}
		}
		f, _ := best.Float64()
		out = append(out, map[string]any{"carrier": ca.name, "carrier_id": ca.id, "quote": f})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i]["quote"].(float64) < out[j]["quote"].(float64)
	})
	return out, nil
}

// DispatchRecommendation GET /api/v1/waybills/{no}/dispatch-recommendation
func (h *Handler) DispatchRecommendation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, no, ok := h.resolve(w, r, "waybill.view")
	if !ok {
		return
	}
	need, err := h.needOf(ctx, id)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取运单失败")
		return
	}
	vehicles, err := h.availableVehicles(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取可用运力失败")
		return
	}
	today := cstMidnight()
	ranked := rankVehicles(vehicles, need, today, false)
	if len(ranked) > 3 {
		ranked = ranked[:3]
	}
	drivers := []map[string]any{}
	drows, err := h.DB.Query(ctx, `
		SELECT id::text, name FROM md_driver
		WHERE is_active AND NOT is_deleted
		  AND id NOT IN (SELECT driver_id FROM ops_waybill WHERE status = ANY($1) AND driver_id IS NOT NULL)
		ORDER BY name, id LIMIT 3`, busyStatuses)
	if err == nil {
		for drows.Next() {
			var did, dname string
			if drows.Scan(&did, &dname) != nil {
				break
			}
			drivers = append(drivers, map[string]any{"driver_id": did, "name": dname})
		}
		drows.Close()
	}
	quotes, _ := h.carrierQuotes(ctx, decimal.NewFromFloat(need.WeightTon))

	var bestVehicle, bestCarrier any
	if len(ranked) > 0 {
		bestVehicle = ranked[0]
	}
	if len(quotes) > 0 {
		bestCarrier = quotes[0]
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waybill_no":         no,
		"cargo":              map[string]any{"weight_ton": need.WeightTon, "volume_cbm": need.VolumeCbm},
		"vehicle_candidates": ranked, "driver_candidates": drivers,
		"carrier_quotes": quotes,
		"best_vehicle":   bestVehicle, "best_carrier": bestCarrier,
	})
}

func cstMidnight() time.Time {
	n := time.Now().In(time.FixedZone("CST", 8*3600))
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location())
}

// DispatchPlan POST /api/v1/waybills/dispatch-plan {waybill_nos:[...]}
//
// 贪心：货量从大到小，每张单挑最紧凑且未占用的车。先排大件是因为大件的
// 可行车少，先满足它才不会被小件把大车占掉。
func (h *Handler) DispatchPlan(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.view") {
		return
	}
	ctx := r.Context()
	if _, err := h.Svc.UserByID(ctx, auth.UserID(r)); err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	var body struct {
		WaybillNos []string `json:"waybill_nos"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	var rows interface {
		Next() bool
		Close()
		Scan(...any) error
	}
	var err error
	if len(body.WaybillNos) > 0 {
		rows, err = h.DB.Query(ctx, `
			SELECT w.id::text, w.waybill_no, COALESCE(w.cargo_weight_ton,0)::float8,
			       COALESCE(w.cargo_volume_cbm,0)::float8,
			       COALESCE(o.is_hazardous,false), COALESCE(o.temperature_range,''), COALESCE(o.business_type,'')
			FROM ops_waybill w LEFT JOIN ops_order o ON o.id = w.order_id
			WHERE w.waybill_no = ANY($1) ORDER BY w.created_at DESC, w.id`, body.WaybillNos)
	} else {
		rows, err = h.DB.Query(ctx, `
			SELECT w.id::text, w.waybill_no, COALESCE(w.cargo_weight_ton,0)::float8,
			       COALESCE(w.cargo_volume_cbm,0)::float8,
			       COALESCE(o.is_hazardous,false), COALESCE(o.temperature_range,''), COALESCE(o.business_type,'')
			FROM ops_waybill w LEFT JOIN ops_order o ON o.id = w.order_id
			WHERE w.status = 'pending_dispatch' ORDER BY w.created_at DESC, w.id LIMIT 200`)
	}
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取运单失败："+err.Error())
		return
	}
	type item struct {
		no   string
		need cargoNeed
	}
	items := []item{}
	for rows.Next() {
		var it item
		var temp, biz string
		var id string
		if rows.Scan(&id, &it.no, &it.need.WeightTon, &it.need.VolumeCbm, &it.need.Hazmat, &temp, &biz) != nil {
			break
		}
		it.need.NeedsReefer = temp != "" || biz == "coldchain"
		items = append(items, it)
	}
	rows.Close()

	vehicles, err := h.availableVehicles(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取可用运力失败")
		return
	}
	// 货量降序；Python sorted 是稳定排序，等量时保留原次序
	sort.SliceStable(items, func(i, j int) bool { return items[i].need.WeightTon > items[j].need.WeightTon })

	today := cstMidnight()
	used := map[string]bool{}
	assignments := []map[string]any{}
	unassigned := []string{}
	for _, it := range items {
		free := make([]vehicleRow, 0, len(vehicles))
		for _, v := range vehicles {
			if !used[v.ID] {
				free = append(free, v)
			}
		}
		ranked := rankVehicles(free, it.need, today, false)
		if len(ranked) == 0 {
			unassigned = append(unassigned, it.no)
			continue
		}
		pick := ranked[0]
		used[pick["vehicle_id"].(string)] = true
		assignments = append(assignments, map[string]any{"waybill_no": it.no, "vehicle": pick})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"assignments": assignments, "unassigned": unassigned, "assigned_count": len(assignments),
	})
}
