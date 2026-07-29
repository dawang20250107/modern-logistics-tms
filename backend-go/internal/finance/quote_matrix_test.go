package finance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/shopspring/decimal"
)

// 计价是钱：六种计费方式的结果必须与既有实现逐分一致（含银行家舍入的边界）。
// 本测试导出一组代表性算例，供与 Django 的 PricingRule.quote 对拍。
func TestExportQuoteMatrix(t *testing.T) {
	d := decimal.RequireFromString
	base := Rule{
		BasePrice: d("100"), MinPrice: d("50"), UnitPrice: d("12.5"),
		MinChargeQty: d("2"), VolumetricFactor: d("0.3333"), FuelSurchargePct: d("0.05"),
		TierPrices: []map[string]any{
			{"min_ton": 0.0, "max_ton": 5.0, "price": 300.0},
			{"min_ton": 5.0, "max_ton": 20.0, "price": 260.0},
			{"min_ton": 20.0, "max_ton": 999.0, "price": 220.0},
		},
	}
	methods := []string{MethodTieredWeight, MethodFlat, MethodPerVolume, MethodPerPiece, MethodPerKm, MethodPerTonKm}
	inputs := []QuoteInput{
		{WeightTon: d("3"), VolumeCbm: d("0"), Quantity: d("0"), DistanceKm: d("0")},
		{WeightTon: d("12.345"), VolumeCbm: d("40"), Quantity: d("13"), DistanceKm: d("1234.5")},
		{WeightTon: d("0"), VolumeCbm: d("0"), Quantity: d("0"), DistanceKm: d("0")},
		{WeightTon: d("25"), VolumeCbm: d("3.5"), Quantity: d("1"), DistanceKm: d("0.5")},
		{WeightTon: d("0.005"), VolumeCbm: d("0.015"), Quantity: d("7"), DistanceKm: d("99.995")},
	}
	out := []map[string]any{}
	for _, m := range methods {
		r := base
		r.ChargeMethod = m
		for _, in := range inputs {
			q := Quote(r, in)
			out = append(out, map[string]any{
				"method": m,
				"in": []string{in.WeightTon.String(), in.VolumeCbm.String(),
					in.Quantity.String(), in.DistanceKm.String()},
				"amount":            q.Amount.String(),
				"freight_amount":    q.FreightAmount.String(),
				"fuel_surcharge":    q.FuelSurcharge.String(),
				"chargeable_weight": q.ChargeableWeight.String(),
				"by_volume":         q.ByVolume,
			})
		}
	}
	b, _ := json.MarshalIndent(out, "", " ")
	if err := os.WriteFile("/tmp/quote_go.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
}
