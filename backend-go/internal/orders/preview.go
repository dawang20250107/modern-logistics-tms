package orders

// 录单辅助的两个无副作用端点：
//   POST /orders/parse-preview  仅解析不落库（前端先看结果再确认）
//   POST /orders/quote          按收入计价规则估价
//
// 对齐 OrderViewSet.{parse_preview, quote} 与 finance.services.estimate_order_quote。

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/finance"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

// ParsePreview POST /api/v1/orders/parse-preview {text}
//
// 除了解析结果，还回两样客服真正用得上的东西：缺了哪些关键信息，
// 以及近 24 小时有没有同电话/同线路的活跃单（防重复下单）。
func (h *Handler) ParsePreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Text string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	text := strings.TrimSpace(body.Text)
	if text == "" {
		httpx.Err(w, http.StatusBadRequest, "INTAKE_EMPTY", "text 必填。")
		return
	}
	parsed := parseTextRule(text)
	meta := map[string]any{"source": "rule"}

	important := []struct{ field, label string }{
		{"origin", "始发地"}, {"destination", "目的地"},
		{"contact_phone", "联系电话"}, {"cargo_weight_ton", "货量"},
	}
	missing := []map[string]string{}
	for _, it := range important {
		if isBlank(parsed[it.field]) {
			missing = append(missing, map[string]string{"field": it.field, "label": it.label})
		}
	}

	phone := str(parsed, "contact_phone")
	origin, dest := str(parsed, "origin"), str(parsed, "destination")
	duplicates := []map[string]any{}
	if phone != "" || (origin != "" && dest != "") {
		// 同电话最强；否则同起终点。已取消的不算重复。
		cond := "o.origin = $2 AND o.destination = $3"
		args := []any{"", origin, dest}
		if phone != "" {
			cond = "o.contact_phone = $1"
			args = []any{phone, "", ""}
		}
		rows, err := h.DB.Query(ctx, `
			SELECT o.id::text, o.order_no, o.status, o.origin, o.destination, o.contact_phone, o.created_at
			FROM ops_order o
			WHERE NOT o.is_deleted AND o.status <> 'cancelled'
			  AND o.created_at >= now() - interval '24 hours' AND `+cond+`
			ORDER BY o.created_at DESC, o.id LIMIT 5`, args...)
		if err == nil {
			for rows.Next() {
				var id, no, status, org, dst, ph string
				var at any
				if rows.Scan(&id, &no, &status, &org, &dst, &ph, &at) != nil {
					break
				}
				duplicates = append(duplicates, map[string]any{
					"id": id, "order_no": no, "status": status,
					"origin": org, "destination": dst, "contact_phone": ph, "created_at": at,
				})
			}
			rows.Close()
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"fields": parsed, "meta": meta, "missing": missing, "duplicates": duplicates,
	})
}

func isBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case float64:
		return x == 0
	case int:
		return x == 0
	}
	return false
}

// Quote POST /api/v1/orders/quote —— 录单自动报价
func (h *Handler) Quote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	weight := decOf(body, "cargo_weight_ton", "weight_ton")
	volume := decOf(body, "cargo_volume_cbm", "volume_cbm")
	quantity := decOf(body, "cargo_quantity", "quantity")
	distance := decOf(body, "distance_km", "")
	customer := str(body, "customer")
	route := str(body, "origin") + "→" + str(body, "destination")

	// 计费重：物理重与体积折算重取大者，与真正落费用时同一口径
	factor := finance.VolumetricFactorTonPerCbm
	chargeable := finance.ChargeableWeight(weight, volume, factor)
	volumetric := volume.Mul(factor)
	base := map[string]any{
		"actual_weight":     f64(weight),
		"volumetric_weight": roundN(f64(volumetric), 3),
		"chargeable_weight": f64(chargeable),
		"by_volume":         volumetric.GreaterThan(weight),
	}

	rule, err := finance.MatchRule(ctx, h.DB, "", "income", customer, "", route, "", cstDay())
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "匹配计价规则失败："+err.Error())
		return
	}
	if rule == nil {
		out := map[string]any{"amount": 0.0, "rule_name": "", "matched": false, "charge_method": ""}
		for k, v := range base {
			out[k] = v
		}
		httpx.JSON(w, http.StatusOK, out)
		return
	}
	res := finance.Quote(*rule, finance.QuoteInput{
		WeightTon: weight, VolumeCbm: volume, Quantity: quantity, DistanceKm: distance,
	})
	out := map[string]any{
		"amount": f64(res.Amount), "rule_name": rule.Name, "matched": true,
		"charge_method": rule.ChargeMethod, "charge_method_label": chargeMethodLabel[rule.ChargeMethod],
	}
	for k, v := range base {
		out[k] = v
	}
	httpx.JSON(w, http.StatusOK, out)
}

var chargeMethodLabel = map[string]string{
	"tiered_weight": "按重量阶梯", "flat": "整车一口价", "per_volume": "按方计费",
	"per_piece": "按件计费", "per_km": "按公里计费", "per_ton_km": "吨公里计费",
}

// decOf 取第一个非空键（兼容两套入参名）
func decOf(m map[string]any, keys ...string) decimal.Decimal {
	for _, k := range keys {
		if k == "" {
			continue
		}
		if v, has := m[k]; has && v != nil {
			switch x := v.(type) {
			case float64:
				return decimal.NewFromFloat(x)
			case string:
				if d, err := decimal.NewFromString(strings.TrimSpace(x)); err == nil {
					return d
				}
			}
		}
	}
	return decimal.Zero
}

func f64(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}
