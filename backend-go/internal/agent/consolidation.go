package agent

// 最后两个工具的原生实现：
//   logistics.dispatch_recommendation   为运单推荐车辆/司机并给出承运商比价
//   logistics.intelligent_consolidation 同向 LTL 小单合并配载 FTL 卡车的降本方案
//
// 对齐 apps/ops/dispatch.{recommend_dispatch, consolidate_and_group_orders}。
// 拼单配载算法本身住在订单域（internal/orders），这里只是把它挂成 AI 工具——
// 同一套算法不该有两份实现，调度台点出来的方案和 AI 说出来的方案必须是同一个。

import (
	"context"
	"fmt"
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
			out, err := orders.ConsolidateByCity(ctx, db, city)
			if err != nil {
				return nil, &toolErr{500, "CONSOLIDATION_FAILED", err.Error()}
			}
			return out, nil
		},
	})
}
