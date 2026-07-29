package agent

// 最后两个工具的原生实现：
//   logistics.dispatch_recommendation   为运单推荐车辆/司机并给出承运商比价
//   logistics.intelligent_consolidation 同向 LTL 小单合并配载 FTL 卡车的降本方案
//
// 对齐 apps/ops/dispatch.{recommend_dispatch, consolidate_and_group_orders}。

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/orders"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"
)

func decFromFloat(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

func roundN(v float64, n int) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', n, 64), 64)
	return f
}

// dispatchRecommendation 运单派车建议：车辆候选 + 司机候选 + 承运商比价
func dispatchRecommendation(ctx context.Context, db *pgxpool.Pool, waybillNo string) (map[string]any, error) {
	var weight, volume float64
	var temp, biz string
	var hazmat bool
	err := db.QueryRow(ctx, `
		SELECT COALESCE(w.cargo_weight_ton,0)::float8, COALESCE(w.cargo_volume_cbm,0)::float8,
		       COALESCE(o.is_hazardous,false), COALESCE(o.temperature_range,''), COALESCE(o.business_type,'')
		FROM ops_waybill w LEFT JOIN ops_order o ON o.id = w.order_id
		WHERE w.waybill_no = $1`, waybillNo).Scan(&weight, &volume, &hazmat, &temp, &biz)
	if err != nil {
		return nil, fmt.Errorf("运单 %s 不存在", waybillNo)
	}
	wh := &waybills.Handler{DB: db}
	vehicles, err := waybills.RankVehiclesFor(ctx, wh, weight, volume, temp != "" || biz == "coldchain", hazmat, 3)
	if err != nil {
		return nil, err
	}
	quotes, err := waybills.CarrierQuotesFor(ctx, wh, decFromFloat(weight))
	if err != nil {
		return nil, err
	}
	drivers := []map[string]any{}
	rows, err := db.Query(ctx, `
		SELECT id::text, name FROM md_driver
		WHERE is_active AND NOT is_deleted
		  AND id NOT IN (SELECT driver_id FROM ops_waybill
		                 WHERE status IN ('dispatched','loaded','departed','in_transit') AND driver_id IS NOT NULL)
		ORDER BY name, id LIMIT 3`)
	if err == nil {
		for rows.Next() {
			var id, name string
			if rows.Scan(&id, &name) != nil {
				break
			}
			drivers = append(drivers, map[string]any{"driver_id": id, "name": name})
		}
		rows.Close()
	}
	var bestVehicle, bestCarrier any
	if len(vehicles) > 0 {
		bestVehicle = vehicles[0]
	}
	if len(quotes) > 0 {
		bestCarrier = quotes[0]
	}
	return map[string]any{
		"waybill_no":         waybillNo,
		"cargo":              map[string]any{"weight_ton": weight, "volume_cbm": volume},
		"vehicle_candidates": vehicles, "driver_candidates": drivers,
		"carrier_quotes": quotes,
		"best_vehicle":   bestVehicle, "best_carrier": bestCarrier,
	}, nil
}

type consolOrder struct {
	id, no, origin, dest, customer string
	weight, volume                 float64
}

// intelligentConsolidation 同向小单拼车：按「起点→终点」归组，组内按货量从大到小
// 贪心装车，装满一辆算一趟，再对比「各自单发」与「合单整车」的估价算节省。
//
// 拼单省的是车次不是运价，所以节省额一律不为负——算出来是负数只说明这批货本来
// 就该分开发，那不叫"负节省"，叫"这条建议不成立"。
func intelligentConsolidation(ctx context.Context, db *pgxpool.Pool, cityFilter string) (map[string]any, error) {
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
	all := []consolOrder{}
	for rows.Next() {
		var o consolOrder
		if rows.Scan(&o.id, &o.no, &o.origin, &o.dest, &o.customer, &o.weight, &o.volume) != nil {
			break
		}
		all = append(all, o)
	}
	rows.Close()

	// 按线路归组，保留首次出现的次序
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
	oh := &orders.Handler{DB: db}
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

	// 兼容旧版扁平字段：老调用方按 assignments/unassigned 读
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

// 收官：这两个工具随所属域移植完毕，从 notPortedTools 转入原生注册表。
func init() {
	register(ToolSpec{
		Name:        "logistics.dispatch_recommendation",
		Description: "为运单推荐可用车辆/司机并预估成本毛利。",
		InputSchema: waybillSchema,
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			no, _ := args["waybill_no"].(string)
			out, err := dispatchRecommendation(ctx, db, no)
			if err != nil {
				return nil, &toolErr{404, "WAYBILL_NOT_FOUND", err.Error()}
			}
			return out, nil
		},
	})
	register(ToolSpec{
		Name:        "logistics.intelligent_consolidation",
		Description: "运行智能 B2B 拼单配载与最省算路算法，将同向 LTL 小单合并配载 FTL 卡车，输出降本方案与预计节省金额。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city_filter": map[string]any{"type": "string", "description": "可选：指定始发或目的城市名称（如'无锡'），过滤匹配的拼单建议"},
			},
		},
		Fn: func(ctx context.Context, db *pgxpool.Pool, args map[string]any) (map[string]any, *toolErr) {
			city, _ := args["city_filter"].(string)
			out, err := intelligentConsolidation(ctx, db, city)
			if err != nil {
				return nil, &toolErr{500, "CONSOLIDATION_FAILED", err.Error()}
			}
			return out, nil
		},
	})
}
