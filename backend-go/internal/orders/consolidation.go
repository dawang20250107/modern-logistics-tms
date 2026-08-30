package orders

// 同向 LTL 小单拼车配载 + 订单池批量排线。
//
// 对齐 apps/ops/dispatch.consolidate_and_group_orders 与
// apps/ops/order_dispatch.plan_dispatch_orders。
//
// 算法住在订单域而不是 AI 域：它是纯业务规则（按线路归组、贪心装车、比价算节省），
// AI 那边只是把它当工具调一下。放在 agent 包会让依赖方向倒过来——订单要用自己的
// 排线算法，就得反过来 import AI 包。

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"
)

type consolOrder struct {
	id, no, origin, dest, customer string
	weight, volume                 float64
}

// ConsolidateByCity 按城市关键字筛出池中订单后拼车（AI 工具用）
func ConsolidateByCity(ctx context.Context, db *pgxpool.Pool, cityFilter string) (map[string]any, error) {
	rows, err := db.Query(ctx, `
		SELECT o.id::text, o.order_no, o.origin, o.destination,
		       COALESCE(c.name,'散客'),
		       COALESCE(o.cargo_weight_ton,0)::float8, COALESCE(o.cargo_volume_cbm,0)::float8
		FROM ops_order o LEFT JOIN md_customer c ON c.id = o.customer_id
		WHERE NOT o.is_deleted AND o.status IN ('pooled','dispatching')
		  AND ($1 = '' OR o.origin ILIKE '%'||$1||'%' OR o.destination ILIKE '%'||$1||'%')
		ORDER BY o.created_at DESC, o.id`, cityFilter)
	if err != nil {
		return nil, err
	}
	return consolidate(ctx, db, scanConsolOrders(rows))
}

// ConsolidateByIDs 按订单 id 列表拼车（调度台勾选后一键排线用）。
// 状态仍限定 pooled/dispatching：已派单的订单不该被重新排进方案里。
func ConsolidateByIDs(ctx context.Context, db *pgxpool.Pool, ids []string) (map[string]any, error) {
	rows, err := db.Query(ctx, `
		SELECT o.id::text, o.order_no, o.origin, o.destination,
		       COALESCE(c.name,'散客'),
		       COALESCE(o.cargo_weight_ton,0)::float8, COALESCE(o.cargo_volume_cbm,0)::float8
		FROM ops_order o LEFT JOIN md_customer c ON c.id = o.customer_id
		WHERE NOT o.is_deleted AND o.status IN ('pooled','dispatching')
		  AND o.id::text = ANY($1)
		ORDER BY o.created_at DESC, o.id`, ids)
	if err != nil {
		return nil, err
	}
	return consolidate(ctx, db, scanConsolOrders(rows))
}

func scanConsolOrders(rows interface {
	Next() bool
	Scan(...any) error
	Close()
}) []consolOrder {
	defer rows.Close()
	out := []consolOrder{}
	for rows.Next() {
		var o consolOrder
		if rows.Scan(&o.id, &o.no, &o.origin, &o.dest, &o.customer, &o.weight, &o.volume) != nil {
			break
		}
		out = append(out, o)
	}
	return out
}

// consolidate 按「起点→终点」归组，组内按货量从大到小贪心装车，装满一辆算一趟，
// 再对比「各自单发」与「合单整车」的估价算节省。
//
// 拼单省的是车次不是运价，所以节省额一律不为负——算出来是负数只说明这批货本来
// 就该分开发，那不叫"负节省"，叫"这条建议不成立"。
func consolidate(ctx context.Context, db *pgxpool.Pool, all []consolOrder) (map[string]any, error) {
	groups := map[string][]consolOrder{}
	routeOrder := []string{}
	for _, o := range all {
		key := orUnknown(o.origin) + "→" + orUnknown(o.dest)
		if _, ok := groups[key]; !ok {
			routeOrder = append(routeOrder, key)
		}
		groups[key] = append(groups[key], o)
	}

	wh := &waybills.Handler{DB: db}
	oh := &Handler{DB: db}
	used := map[string]bool{}
	trips := []map[string]any{}
	unassigned := []map[string]any{}

	for _, route := range routeOrder {
		grp := groups[route]
		origin, dest := splitRoute(route)
		sort.SliceStable(grp, func(i, j int) bool { return grp[i].weight > grp[j].weight })

		for len(grp) > 0 {
			// 用一个"大重货"样板去找当下最大的空闲车，作为本趟的容量上限
			free, err := waybills.RankVehiclesForExcluding(ctx, wh, 15, 40, false, false, used)
			if err != nil || len(free) == 0 {
				for _, o := range grp {
					unassigned = append(unassigned, map[string]any{
						"order_id": o.id, "order_no": o.no, "route": route})
				}
				break
			}
			pick := free[0]
			capT, capV, err := vehicleCapacity(ctx, db, pick["vehicle_id"].(string))
			if err != nil {
				break
			}
			curW, curV := 0.0, 0.0
			loaded := []consolOrder{}
			rest := []consolOrder{}
			for _, o := range grp {
				if curW+o.weight <= capT && curV+o.volume <= capV {
					loaded = append(loaded, o)
					curW += o.weight
					curV += o.volume
				} else {
					rest = append(rest, o)
				}
			}
			if len(loaded) == 0 {
				// 单件就装不下：这单没法拼，挑出来单列
				unassigned = append(unassigned, map[string]any{
					"order_id": grp[0].id, "order_no": grp[0].no, "route": route})
				grp = grp[1:]
				continue
			}
			used[pick["vehicle_id"].(string)] = true

			consolidated := floatOf(oh.FreightQuote(ctx, origin, dest, curW, curV)["avg"])
			sep := 0.0
			items := make([]map[string]any, 0, len(loaded))
			for _, o := range loaded {
				sep += floatOf(oh.FreightQuote(ctx, o.origin, o.dest, o.weight, o.volume)["avg"])
				items = append(items, map[string]any{
					"order_id": o.id, "order_no": o.no,
					"weight_ton": o.weight, "volume_cbm": o.volume, "customer_name": o.customer,
				})
			}
			saved := roundN(sep-consolidated, 2)
			if saved < 0 {
				saved = 0
			}
			trips = append(trips, map[string]any{
				"route": route, "origin": origin, "destination": dest,
				"orders":           items,
				"total_weight_ton": roundN(curW, 2), "total_volume_cbm": roundN(curV, 2),
				"vehicle": map[string]any{
					"id": pick["vehicle_id"], "plate_no": pick["plate_no"],
					"load_capacity_ton": capT, "volume_capacity_cbm": capV,
				},
				"separate_cost": roundN(sep, 2), "consolidated_cost": roundN(consolidated, 2),
				"money_saved": saved,
			})
			grp = rest
		}
	}

	// 兼容扁平字段：调度台与老调用方按 assignments/unassigned 读
	flat := []map[string]any{}
	assigned := 0
	totalSaving := 0.0
	for _, t := range trips {
		v := t["vehicle"].(map[string]any)
		for _, o := range t["orders"].([]map[string]any) {
			flat = append(flat, map[string]any{
				"order_id": o["order_id"], "order_no": o["order_no"],
				"route": t["route"], "weight_ton": o["weight_ton"],
				"vehicle": map[string]any{
					"vehicle_id": v["id"], "plate_no": v["plate_no"],
					"slack": 0.0, "utilization": 1.0,
					"compliance": []string{}, "compliance_ok": true,
				},
			})
			assigned++
		}
		totalSaving += t["money_saved"].(float64)
	}
	flatUnassigned := make([]map[string]any, 0, len(unassigned))
	for _, u := range unassigned {
		flatUnassigned = append(flatUnassigned, map[string]any{
			"order_id": u["order_id"], "order_no": u["order_no"]})
	}
	return map[string]any{
		"consolidated_count": len(trips), "unassigned_count": len(unassigned),
		"consolidated_trips": trips, "unassigned_orders": unassigned,
		"estimated_total_saving": roundN(totalSaving, 2),
		"assigned_count":         assigned, "assignments": flat, "unassigned": flatUnassigned,
	}, nil
}

func vehicleCapacity(ctx context.Context, db *pgxpool.Pool, id string) (float64, float64, error) {
	var capT, capV float64
	err := db.QueryRow(ctx, `
		SELECT COALESCE(load_capacity_ton,0)::float8, COALESCE(volume_capacity_cbm,0)::float8
		FROM md_vehicle WHERE id=$1::uuid`, id).Scan(&capT, &capV)
	return capT, capV, err
}

func orUnknown(s string) string {
	if s == "" {
		return "未知"
	}
	return s
}

func splitRoute(route string) (string, string) {
	for i := 0; i+3 <= len(route); i++ {
		if route[i:i+3] == "→" {
			return route[:i], route[i+3:]
		}
	}
	return route, ""
}

func floatOf(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// DispatchPlan POST /api/v1/orders/dispatch-plan {ids:[...]}
//
// 订单池批量智能排线：同向小单合成整车需求 → 推荐承运商 + 整车报价。
// 只出方案不落库，调度员看过再决定派不派——排线是建议，派单才是动作。
func (h *Handler) DispatchPlan(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "waybill.view") {
		return
	}
	ctx := r.Context()
	var body struct {
		IDs []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.IDs) == 0 {
		httpx.Err(w, http.StatusBadRequest, "IDS_REQUIRED", "ids 必填。")
		return
	}
	plan, err := ConsolidateByIDs(ctx, h.DB, body.IDs)
	if err != nil {
		httpx.Fail(w, r, "INTERNAL", "排线失败", err)
		return
	}
	// 每条合并线路配上承运商推荐：拼出整车之后要解决的是「谁来拉」，不是「用哪台自有车」
	trips, _ := plan["consolidated_trips"].([]map[string]any)
	for _, t := range trips {
		origin, _ := t["origin"].(string)
		dest, _ := t["destination"].(string)
		wt, _ := t["total_weight_ton"].(float64)
		cands, err := h.scoreCarriers(ctx, origin, dest, decimal.NewFromFloat(wt), 3)
		if err != nil {
			continue
		}
		t["carrier_candidates"] = cands
		if len(cands) > 0 {
			t["carrier_recommendation"] = recommendationOf(cands[0])
		} else {
			t["carrier_recommendation"] = nil
		}
	}
	httpx.JSON(w, http.StatusOK, plan)
}
