package orders

// AI 派单建议：GET /api/v1/orders/{id}/dispatch-suggestion
//
// 产品原则是「找合适承运商」而不是「找车」：默认推外包承运商，网货平台辅助，
// 自营车兜底。承运商按本线路的履约表现打分排序，给出建议价区间 + 风险说明 +
// 是否需主管确认——建议态，不自动派。
//
// 对齐 apps/ops/order_dispatch.recommend_dispatch_for_order 与
// apps/ops/carrier_scoring.{score_carriers, carrier_recommendation}。

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/finance"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"
)

// scoreWeights 评分权重（合计 100），对应产品需求里的推荐评分维度表
var scoreWeights = []struct {
	Key    string
	Weight float64
}{
	{"route_familiarity", 20}, // 常跑线路
	{"price_reasonable", 20},  // 报价合理
	{"on_time", 15},           // 准班率
	{"low_exception", 15},     // 异常率低
	{"receipt_timely", 10},    // 回单及时
	{"responsiveness", 10},    // 响应速度（暂无采集，给中性基线）
	{"compliance", 10},        // 合规资质
}

// 无历史数据时的中性基线：新承运商不该被一刀切压到 0 分
const (
	baseOnTime         = 0.85
	baseLowException   = 0.90
	baseReceiptTimely  = 0.88
	baseResponsiveness = 0.80
)

type carrierPerf struct {
	Deals, RouteHits                             int
	OnTimeRate, ExceptionRate, ReceiptTimelyRate float64
	RecentDealPrice                              *float64
	HasHistory                                   bool
}

// carrierPerformance 近 90 天该承运商的经营表现（整体 + 本线路）
func (h *Handler) carrierPerformance(ctx context.Context, carrierID, origin, dest string) (carrierPerf, error) {
	var p carrierPerf
	var timedTotal, onTimeHits, excTotal, doneTotal, receiptHits int
	var lanePayable *float64
	err := h.DB.QueryRow(ctx, `
		WITH w AS (
		  SELECT * FROM ops_waybill
		  WHERE carrier_id=$1::uuid AND created_at >= now() - interval '90 days' AND status <> 'voided'
		), route AS (
		  SELECT * FROM w WHERE $2 <> '' AND $3 <> '' AND origin = $2 AND destination = $3
		)
		SELECT (SELECT count(*) FROM w),
		  (SELECT count(*) FROM w WHERE planned_arrival IS NOT NULL AND arrived_at IS NOT NULL),
		  (SELECT count(*) FROM w WHERE planned_arrival IS NOT NULL AND arrived_at IS NOT NULL
		     AND arrived_at <= planned_arrival),
		  (SELECT count(*) FROM w WHERE EXISTS (SELECT 1 FROM ops_exception x WHERE x.waybill_id = w.id)),
		  (SELECT count(*) FROM w WHERE status IN ('arrived','signed','delivered','settled')),
		  (SELECT count(*) FROM w WHERE status IN ('arrived','signed','delivered','settled')
		     AND receipt_status IN ('returned','audited')),
		  (SELECT count(*) FROM route),
		  (SELECT avg(pay)::float8 FROM (
		     SELECT (SELECT sum(e.amount) FROM fin_expense_record e
		             WHERE e.waybill_id = route.id AND e.direction='payable') AS pay
		     FROM route) s WHERE pay IS NOT NULL)`,
		carrierID, origin, dest).
		Scan(&p.Deals, &timedTotal, &onTimeHits, &excTotal, &doneTotal, &receiptHits, &p.RouteHits, &lanePayable)
	if err != nil {
		return p, err
	}
	p.OnTimeRate = rateOr(onTimeHits, timedTotal, baseOnTime)
	p.ExceptionRate = rateOr(excTotal, p.Deals, float64(1)-baseLowException)
	p.ReceiptTimelyRate = rateOr(receiptHits, doneTotal, baseReceiptTimely)
	p.HasHistory = p.Deals > 0
	if lanePayable != nil {
		v := pyRoundF(*lanePayable, 2)
		p.RecentDealPrice = &v
	}
	return p, nil
}

func rateOr(num, den int, def float64) float64 {
	if den == 0 {
		return def
	}
	return pyRoundF(float64(num)/float64(den), 4)
}

func pyRoundF(v float64, n int) float64 { return roundN(v, n) }

// roundN 复刻 Python round(float, n)：十进制半偶入
func roundN(v float64, n int) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', n, 64), 64)
	return f
}

// cstDay 项目时区的今天零点（计价规则生效期比对用）
func cstDay() time.Time {
	n := time.Now().In(time.FixedZone("CST", 8*3600))
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location())
}

// lanePrice 线路价库中该承运商在此线路的报价（优先推荐/常用，再取低价）
type lanePriceRow struct {
	StandardPrice, LastDealPrice *float64
	Recommended, Preferred       bool
}

func (h *Handler) lanePrice(ctx context.Context, carrierID, origin, dest string) *lanePriceRow {
	if origin == "" || dest == "" {
		return nil
	}
	var l lanePriceRow
	err := h.DB.QueryRow(ctx, `
		SELECT standard_price::float8, last_deal_price::float8, is_recommended, is_preferred
		FROM md_carrier_lane_price
		WHERE carrier_id=$1::uuid AND origin_city=$2 AND dest_city=$3 AND is_active AND NOT is_deleted
		ORDER BY is_recommended DESC, is_preferred DESC, standard_price, id
		LIMIT 1`, carrierID, origin, dest).
		Scan(&l.StandardPrice, &l.LastDealPrice, &l.Recommended, &l.Preferred)
	if err != nil {
		return nil
	}
	return &l
}

type candidate struct {
	id, name, grade string
	quote           *float64
	perf            carrierPerf
	lane            *lanePriceRow
}

// scoreCarriers 为订单在目标线路上给候选承运商打分排序（价低者未必最优）
func (h *Handler) scoreCarriers(ctx context.Context, origin, dest string, weight decimal.Decimal, top int) ([]map[string]any, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT id::text, name, COALESCE(grade,'B') FROM md_carrier
		WHERE is_active AND NOT blacklisted AND NOT is_deleted ORDER BY code, id`)
	if err != nil {
		return nil, err
	}
	type c struct{ id, name, grade string }
	list := []c{}
	for rows.Next() {
		var x c
		if rows.Scan(&x.id, &x.name, &x.grade) != nil {
			break
		}
		list = append(list, x)
	}
	rows.Close()

	cands := []candidate{}
	for _, ca := range list {
		blocked, err := h.carrierBlocked(ctx, ca.id)
		if err != nil || blocked {
			continue // 黑名单/停用/资质过期不进推荐——推了也会在派单时被硬阻断
		}
		lane := h.lanePrice(ctx, ca.id, origin, dest)
		var quote *float64
		if lane != nil && lane.StandardPrice != nil && *lane.StandardPrice != 0 {
			quote = lane.StandardPrice
		} else if rules, err := finance.CostRulesFor(ctx, h.DB, ca.id, cstDay()); err == nil && len(rules) > 0 {
			var best *decimal.Decimal
			for _, rule := range rules {
				amt := finance.Quote(rule, finance.QuoteInput{WeightTon: weight}).Amount
				if best == nil || amt.LessThan(*best) {
					v := amt
					best = &v
				}
			}
			f, _ := best.Float64()
			quote = &f
		}
		perf, err := h.carrierPerformance(ctx, ca.id, origin, dest)
		if err != nil {
			continue
		}
		if perf.RecentDealPrice == nil && lane != nil && lane.LastDealPrice != nil && *lane.LastDealPrice != 0 {
			v := *lane.LastDealPrice
			perf.RecentDealPrice = &v
		}
		cands = append(cands, candidate{ca.id, ca.name, ca.grade, quote, perf, lane})
	}
	if len(cands) == 0 {
		return []map[string]any{}, nil
	}

	lo, hi, cheapest := priceBand(cands)
	out := []map[string]any{}
	for _, c := range cands {
		pricePos := 0.5 // 无报价或全场同价：给中性分，不奖不罚
		if c.quote != nil && hi != lo {
			pricePos = 1.0 - (*c.quote-lo)/(hi-lo)
		}
		score, breakdown := scoreOf(c.perf, pricePos)
		isCheapest := c.quote != nil && cheapest != nil && math.Abs(*c.quote-*cheapest) < 1e-6
		risk, label, notes := riskAndLabel(c.grade, c.perf, isCheapest)
		var band []float64
		base := 0.0
		if c.perf.RecentDealPrice != nil {
			base = *c.perf.RecentDealPrice
		} else if c.quote != nil {
			base = *c.quote
		}
		if base != 0 {
			band = []float64{roundN(base*0.97, 0), roundN(base*1.03, 0)}
		}
		row := map[string]any{
			"carrier_id": c.id, "carrier": c.name, "carrier_grade": c.grade,
			"quote":                floatOrNil(c.quote),
			"from_lane_price":      c.lane != nil && c.lane.StandardPrice != nil && *c.lane.StandardPrice != 0,
			"lane_preferred":       c.lane != nil && (c.lane.Recommended || c.lane.Preferred),
			"recent_deal_price":    floatOrNil(c.perf.RecentDealPrice),
			"suggested_price_band": bandOrNil(band),
			"deals":                c.perf.Deals, "route_hits": c.perf.RouteHits,
			"on_time_rate":        c.perf.OnTimeRate,
			"exception_rate":      c.perf.ExceptionRate,
			"receipt_timely_rate": c.perf.ReceiptTimelyRate,
			"score":               score, "score_breakdown": breakdown,
			"risk_level": risk, "label": label, "risk_notes": notes,
		}
		out = append(out, row)
	}
	// 综合分降序；同分价低者优先
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i]["score"].(int), out[j]["score"].(int)
		if si != sj {
			return si > sj
		}
		return quoteOrMax(out[i]) < quoteOrMax(out[j])
	})
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out, nil
}

func quoteOrMax(m map[string]any) float64 {
	if q, ok := m["quote"].(float64); ok {
		return q
	}
	return 1e12
}

func floatOrNil(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func bandOrNil(b []float64) any {
	if b == nil {
		return nil
	}
	return b
}

func priceBand(cands []candidate) (lo, hi float64, cheapest *float64) {
	first := true
	for _, c := range cands {
		if c.quote == nil {
			continue
		}
		if first {
			lo, hi = *c.quote, *c.quote
			first = false
			continue
		}
		lo = math.Min(lo, *c.quote)
		hi = math.Max(hi, *c.quote)
	}
	if !first {
		v := lo
		cheapest = &v
	}
	return lo, hi, cheapest
}

// carrierBlocked 承运商是否被硬阻断（对齐 Carrier.dispatch_block_reason）
func (h *Handler) carrierBlocked(ctx context.Context, carrierID string) (bool, error) {
	var blocked bool
	err := h.DB.QueryRow(ctx, `
		SELECT (NOT is_active) OR blacklisted
		    OR (qualification_expiry IS NOT NULL AND qualification_expiry < (now() AT TIME ZONE 'Asia/Shanghai')::date)
		FROM md_carrier WHERE id=$1::uuid`, carrierID).Scan(&blocked)
	return blocked, err
}

func scoreOf(p carrierPerf, pricePos float64) (int, map[string]float64) {
	parts := map[string]float64{
		"route_familiarity": math.Min(float64(p.RouteHits)/5.0, 1.0),
		"price_reasonable":  pricePos,
		"on_time":           p.OnTimeRate,
		"low_exception":     1.0 - p.ExceptionRate,
		"receipt_timely":    p.ReceiptTimelyRate,
		"responsiveness":    baseResponsiveness,
		"compliance":        1.0, // 进入评分的均已过硬阻断；分级在风险标签里微调
	}
	total := 0.0
	for _, w := range scoreWeights {
		total += parts[w.Key] * w.Weight
	}
	rounded := map[string]float64{}
	for k, v := range parts {
		rounded[k] = roundN(v, 3)
	}
	return int(roundN(total, 0)), rounded
}

func riskAndLabel(grade string, p carrierPerf, isCheapest bool) (string, string, []string) {
	notes := []string{}
	if p.ExceptionRate >= 0.10 {
		notes = append(notes, fmt.Sprintf("异常率偏高（%s）", pct(p.ExceptionRate)))
	}
	if p.OnTimeRate < 0.85 && p.HasHistory {
		notes = append(notes, fmt.Sprintf("准班率偏低（%s）", pct(p.OnTimeRate)))
	}
	if grade == "C" || grade == "D" {
		notes = append(notes, "综合评级 "+grade+"，需关注")
	}
	if !p.HasHistory {
		notes = append(notes, "近 90 天无成交历史，建议先试单")
	}
	risk := "low"
	switch {
	case p.ExceptionRate >= 0.10 || p.OnTimeRate < 0.80 || grade == "D":
		risk = "high"
	case p.ExceptionRate >= 0.05 || grade == "C" || !p.HasHistory:
		risk = "medium"
	}
	var label string
	switch {
	case isCheapest && (p.ExceptionRate >= 0.06 || p.OnTimeRate < 0.85):
		label = "低价有风险"
		notes = append([]string{"报价最低但履约有波动，议价需谨慎"}, notes...)
	case p.OnTimeRate >= 0.97 && p.ExceptionRate <= 0.03 && p.HasHistory:
		label = "高服务"
	case risk == "low" && p.RouteHits >= 3:
		label = "推荐"
	default:
		label = "备选"
	}
	return risk, label, notes
}

// pct 复刻 Python 的 f"{x:.0%}"
func pct(v float64) string { return fmt.Sprintf("%.0f%%", roundN(v*100, 0)) }

// DispatchSuggestion GET /api/v1/orders/{id}/dispatch-suggestion
func (h *Handler) DispatchSuggestion(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveOrder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var orderNo, origin, dest, temp, biz, priority string
	var weight, volume float64
	var hazmat bool
	if err := h.DB.QueryRow(ctx, `
		SELECT order_no, origin, destination, COALESCE(cargo_weight_ton,0)::float8,
		       COALESCE(cargo_volume_cbm,0)::float8, is_hazardous,
		       COALESCE(temperature_range,''), COALESCE(business_type,''), COALESCE(priority,'')
		FROM ops_order WHERE id=$1::uuid`, id).
		Scan(&orderNo, &origin, &dest, &weight, &volume, &hazmat, &temp, &biz, &priority); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取订单失败")
		return
	}
	wDec := decimal.NewFromFloat(weight)
	carrierRows, err := h.scoreCarriers(ctx, origin, dest, wDec, 6)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "承运商评分失败："+err.Error())
		return
	}
	wh := &waybills.Handler{DB: h.DB, Svc: h.Svc}
	vehicles, _ := waybills.RankVehiclesFor(ctx, wh, weight, volume, temp != "" || biz == "coldchain", hazmat, 3)
	quotes, _ := waybills.CarrierQuotesFor(ctx, wh, wDec)
	ymm := h.FreightQuote(ctx, origin, dest, weight, volume)

	signals := []map[string]any{}
	if hazmat {
		signals = append(signals, map[string]any{"type": "hazardous", "level": "high",
			"note": "危险品，需具备危运资质的承运商/车辆"})
	}
	if biz == "coldchain" || temp != "" {
		t := temp
		if t == "" {
			t = "未填"
		}
		signals = append(signals, map[string]any{"type": "coldchain", "level": "high",
			"note": "冷链温区 " + t + "，需温控车"})
	}
	if priority == "urgent" || priority == "vip" {
		signals = append(signals, map[string]any{"type": "priority", "level": "medium",
			"note": priority + " 优先级，建议优先调度并预留时效"})
	}

	// 派单类型建议：外包承运商优先 → 网货平台辅助 → 自营车兜底
	suggested := "third_party"
	switch {
	case len(carrierRows) > 0:
		suggested = "third_party"
	case ymm["avg"] != nil:
		suggested = "platform"
	case len(vehicles) > 0:
		suggested = "own_vehicle"
	}

	var recommendation any
	if len(carrierRows) > 0 {
		recommendation = recommendationOf(carrierRows[0])
	}
	var bestVehicle, bestCarrier any
	if len(vehicles) > 0 {
		bestVehicle = vehicles[0]
	}
	if len(quotes) > 0 {
		bestCarrier = quotes[0]
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"order_no":                orderNo,
		"cargo":                   map[string]any{"weight_ton": weight, "volume_cbm": volume},
		"carrier_recommendations": carrierRows,
		"recommendation":          recommendation,
		"vehicle_candidates":      vehicles,
		"carrier_quotes":          quotes,
		"ymm_quote":               ymm,
		"external_signals":        signals,
		"suggested_dispatch_type": suggested,
		"best_vehicle":            bestVehicle,
		"best_carrier":            bestCarrier,
	})
}

// recommendationOf 把首选承运商折成一句可执行结论 + 是否需主管确认
func recommendationOf(top map[string]any) map[string]any {
	reasons := []string{}
	routeHits := top["route_hits"].(int)
	onTime := top["on_time_rate"].(float64)
	excRate := top["exception_rate"].(float64)
	if routeHits != 0 {
		reasons = append(reasons, fmt.Sprintf("近 90 天该线路成交 %d 单，准班率 %s", routeHits, pct(onTime)))
	}
	if p, ok := top["recent_deal_price"].(float64); ok && p != 0 {
		reasons = append(reasons, fmt.Sprintf("最近成交价约 ¥%.0f", roundN(p, 0)))
	}
	if rt, ok := top["receipt_timely_rate"].(float64); ok && rt != 0 {
		reasons = append(reasons, "回单及时率 "+pct(rt))
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "暂无历史成交，建议先试单或电话询价确认")
	}
	needsApproval := top["risk_level"].(string) == "high" || routeHits == 0 || excRate >= 0.08
	return map[string]any{
		"carrier_id": top["carrier_id"], "carrier": top["carrier"],
		"suggested_price_band": top["suggested_price_band"],
		"risk_level":           top["risk_level"], "label": top["label"],
		"reasons": reasons, "risk_notes": top["risk_notes"],
		"needs_approval": needsApproval,
	}
}
