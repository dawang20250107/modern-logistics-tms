package finance

// 计价数学的逐分回归。
//
// 为什么这份文件必须存在：迁移时财务域是**重新设计而非等价移植**，
// 理由是旧实现有三处会直接算错钱。当时的验证方式写在 PR 里——
// 「计价数学逐分对拍 + 业务规则逐条断言」——但那些对拍是**迁移时手工做的**，
// 一条也没落成测试。也就是说，今天谁去改 pricing.go，
// 没有任何东西会绊住他，而这里算错一分钱，对账时就是要人命的差异。
//
// 覆盖六种计费方式，外加四条容易被"顺手优化"掉的规则：
// 抛重取大、最低计费量、最低价下限、银行家舍入。

import (
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// mustEq 金额比较必须按 decimal 的值比，不能比字符串：
// "100" 与 "100.00" 是同一个金额，字符串却不等。
func mustEq(t *testing.T, got, want decimal.Decimal, what string) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s：期望 %s，实际 %s（差 %s）", what, want, got, got.Sub(want))
	}
}

// ── 六种计费方式各自的基本盘 ──────────────────────────────

func TestQuoteFlat(t *testing.T) {
	// 整车一口价：给多少货都是这个价，计量部分完全不参与
	r := Rule{ChargeMethod: MethodFlat, BasePrice: dec("3500"), FuelSurchargePct: decimal.Zero}
	got := Quote(r, QuoteInput{WeightTon: dec("18"), VolumeCbm: dec("60"), Quantity: dec("100"), DistanceKm: dec("1200")})
	mustEq(t, got.Amount, dec("3500.00"), "整车一口价")
	mustEq(t, got.FreightAmount, dec("3500.00"), "运费")
	mustEq(t, got.FuelSurcharge, dec("0.00"), "燃油附加")
}

func TestQuotePerVolume(t *testing.T) {
	// 按方：起步价 + 单价×方数
	r := Rule{ChargeMethod: MethodPerVolume, BasePrice: dec("200"), UnitPrice: dec("85")}
	got := Quote(r, QuoteInput{VolumeCbm: dec("12.5")})
	// 200 + 85 × 12.5 = 1262.50
	mustEq(t, got.Amount, dec("1262.50"), "按方")
	if v, _ := got.Detail["billable_volume"].(float64); v != 12.5 {
		t.Errorf("计费方数应为 12.5，实际 %v", got.Detail["billable_volume"])
	}
}

func TestQuotePerPiece(t *testing.T) {
	r := Rule{ChargeMethod: MethodPerPiece, BasePrice: dec("50"), UnitPrice: dec("3.5")}
	got := Quote(r, QuoteInput{Quantity: dec("120")})
	// 50 + 3.5 × 120 = 470
	mustEq(t, got.Amount, dec("470.00"), "按件")
}

func TestQuotePerKm(t *testing.T) {
	r := Rule{ChargeMethod: MethodPerKm, BasePrice: dec("300"), UnitPrice: dec("6.8")}
	got := Quote(r, QuoteInput{DistanceKm: dec("1450")})
	// 300 + 6.8 × 1450 = 10160
	mustEq(t, got.Amount, dec("10160.00"), "按公里")
}

func TestQuotePerTonKm(t *testing.T) {
	// 吨公里：单价 × 计费重 × 里程。计费重走抛重取大，所以这里同时验两件事。
	r := Rule{ChargeMethod: MethodPerTonKm, BasePrice: dec("100"), UnitPrice: dec("0.45"),
		VolumetricFactor: dec("0.333")}
	got := Quote(r, QuoteInput{WeightTon: dec("8"), VolumeCbm: dec("10"), DistanceKm: dec("600")})
	// 体积重 = 10 × 0.333 = 3.33 < 8 → 计费重 8
	// 100 + 0.45 × 8 × 600 = 100 + 2160 = 2260
	mustEq(t, got.Amount, dec("2260.00"), "吨公里")
	mustEq(t, got.ChargeableWeight, dec("8"), "计费重取实重")
	if got.ByVolume {
		t.Error("实重大于体积重时 ByVolume 应为 false")
	}
}

func TestQuoteTieredWeight(t *testing.T) {
	// 阶梯价：落在哪一档就整段按那一档的吨价算（不是分段累进）
	r := Rule{
		ChargeMethod: MethodTieredWeight, BasePrice: dec("0"),
		TierPrices: []map[string]any{
			{"min_ton": "0", "max_ton": "5", "price": "260"},
			{"min_ton": "5", "max_ton": "15", "price": "220"},
			{"min_ton": "15", "max_ton": "30", "price": "190"},
		},
	}
	for _, c := range []struct{ ton, want string }{
		{"3", "780.00"},   // 3 × 260
		{"10", "2200.00"}, // 10 × 220
		{"20", "3800.00"}, // 20 × 190
	} {
		got := Quote(r, QuoteInput{WeightTon: dec(c.ton)})
		mustEq(t, got.Amount, dec(c.want), "阶梯价 "+c.ton+" 吨")
	}
}

func TestTieredWeightBoundaryTakesLowerTier(t *testing.T) {
	// 边界值 5 吨同时满足 [0,5] 与 [5,15]。实现按声明顺序**首个命中**，
	// 于是取的是第一档。这不是"随便哪个都行"——它决定 5 吨整这一单收 1300 还是 1100，
	// 两百块的差。把行为钉住，将来谁调整档位顺序会立刻看见。
	r := Rule{
		ChargeMethod: MethodTieredWeight,
		TierPrices: []map[string]any{
			{"min_ton": "0", "max_ton": "5", "price": "260"},
			{"min_ton": "5", "max_ton": "15", "price": "220"},
		},
	}
	got := Quote(r, QuoteInput{WeightTon: dec("5")})
	mustEq(t, got.Amount, dec("1300.00"), "5 吨整（边界取首个命中的档）")
}

func TestTieredWeightNoMatchingTierChargesBaseOnly(t *testing.T) {
	// 超出所有档位时 pricePerTon 保持 0，只收起步价。
	// 这是个值得钉住的行为：它意味着**档位没配全会静默少收钱**，
	// 而不是报错。谁要改成"超出即报错"，这条会红，提醒他那是行为变更。
	r := Rule{
		ChargeMethod: MethodTieredWeight, BasePrice: dec("500"),
		TierPrices: []map[string]any{{"min_ton": "0", "max_ton": "10", "price": "300"}},
	}
	got := Quote(r, QuoteInput{WeightTon: dec("50")})
	mustEq(t, got.Amount, dec("500.00"), "超出所有档位只收起步价")
}

// ── 四条容易被"顺手优化"掉的规则 ────────────────────────

func TestChargeableWeightTakesVolumetricWhenBulky(t *testing.T) {
	// 泡货：体积重大于实重时按体积重收，否则轻抛货就白拉了
	r := Rule{ChargeMethod: MethodTieredWeight, VolumetricFactor: dec("0.333"),
		TierPrices: []map[string]any{{"min_ton": "0", "max_ton": "999", "price": "100"}}}
	got := Quote(r, QuoteInput{WeightTon: dec("2"), VolumeCbm: dec("30")})
	// 体积重 = 30 × 0.333 = 9.99 > 2 → 计费重 9.99
	mustEq(t, got.ChargeableWeight, dec("9.99"), "计费重取体积重")
	mustEq(t, got.Amount, dec("999.00"), "按体积重计价")
	if !got.ByVolume {
		t.Error("体积重大于实重时 ByVolume 应为 true")
	}
}

func TestVolumetricFactorDefaultsWhenZero(t *testing.T) {
	// 规则没配抛比时回落到全局默认 0.333，而不是当成 0
	//（当成 0 的话体积重恒为 0，所有泡货一律按净重收——静默少收钱）
	r := Rule{ChargeMethod: MethodTieredWeight, VolumetricFactor: decimal.Zero,
		TierPrices: []map[string]any{{"min_ton": "0", "max_ton": "999", "price": "100"}}}
	got := Quote(r, QuoteInput{WeightTon: dec("1"), VolumeCbm: dec("30")})
	mustEq(t, got.ChargeableWeight, dec("9.99"), "抛比缺省应回落 0.333")
}

func TestMinChargeQtyFloorsBillableAmount(t *testing.T) {
	// 最低计费量：不足起收量按起收量算
	r := Rule{ChargeMethod: MethodPerVolume, BasePrice: decimal.Zero, UnitPrice: dec("100"),
		MinChargeQty: dec("5")}
	got := Quote(r, QuoteInput{VolumeCbm: dec("2")})
	mustEq(t, got.Amount, dec("500.00"), "2 方按 5 方起收")

	// 超过起收量则按实际
	got = Quote(r, QuoteInput{VolumeCbm: dec("8")})
	mustEq(t, got.Amount, dec("800.00"), "8 方按实际")
}

func TestMinPriceFloorsFreightBeforeFuel(t *testing.T) {
	// 最低价是**运费的**下限，燃油附加在它之上叠加，不是"总额下限"。
	// 顺序反了会少收燃油费。
	r := Rule{ChargeMethod: MethodPerPiece, BasePrice: decimal.Zero, UnitPrice: dec("1"),
		MinPrice: dec("200"), FuelSurchargePct: dec("0.1")}
	got := Quote(r, QuoteInput{Quantity: dec("10")}) // 算出来 10，被抬到 200
	mustEq(t, got.FreightAmount, dec("200.00"), "运费被最低价抬起")
	mustEq(t, got.FuelSurcharge, dec("20.00"), "燃油按抬起后的运费算")
	mustEq(t, got.Amount, dec("220.00"), "总额 = 运费 + 燃油")
}

func TestFuelSurchargeIsPercentOfFreight(t *testing.T) {
	r := Rule{ChargeMethod: MethodFlat, BasePrice: dec("1000"), FuelSurchargePct: dec("0.025")}
	got := Quote(r, QuoteInput{})
	mustEq(t, got.FreightAmount, dec("1000.00"), "运费")
	mustEq(t, got.FuelSurcharge, dec("25.00"), "燃油 2.5%")
	mustEq(t, got.Amount, dec("1025.00"), "总额")
}

// ── 舍入：差一分钱就是对账差异 ──────────────────────────

func TestBankersRoundingOnHalfCent(t *testing.T) {
	// bank2 是银行家舍入（ROUND_HALF_EVEN），不是四舍五入。
	// 复刻的是 Python 侧 round(Decimal, 2) 的行为——换成 ROUND_HALF_UP
	// 会在 .005 边界上与既有账目逐笔差一分钱。
	for _, c := range []struct{ in, want string }{
		{"1.005", "1.00"}, // 向偶数舍：0 是偶数
		{"1.015", "1.02"}, // 向偶数舍：2 是偶数
		{"1.025", "1.02"},
		{"1.035", "1.04"},
		{"2.675", "2.68"},
	} {
		if got := bank2(dec(c.in)); !got.Equal(dec(c.want)) {
			t.Errorf("bank2(%s)：期望 %s（银行家舍入），实际 %s —— "+
				"换成四舍五入会与既有账目逐笔差一分钱", c.in, c.want, got)
		}
	}
}

func TestQuoteRoundsToTwoDecimals(t *testing.T) {
	// 单价带三位小数时，总额必须收敛到分
	r := Rule{ChargeMethod: MethodPerPiece, UnitPrice: dec("0.333"), FuelSurchargePct: dec("0.033")}
	got := Quote(r, QuoteInput{Quantity: dec("7")})
	// 运费 = 0.333 × 7 = 2.331 → 2.33
	// 燃油 = 2.331 × 0.033 = 0.076923 → 0.08
	// 总额 = 2.331 + 0.076923 = 2.407923 → 2.41
	mustEq(t, got.FreightAmount, dec("2.33"), "运费收敛到分")
	mustEq(t, got.FuelSurcharge, dec("0.08"), "燃油收敛到分")
	mustEq(t, got.Amount, dec("2.41"), "总额按未舍入的中间量算再收敛")

}

func TestAmountRoundsTheSumNotTheSumOfRounded(t *testing.T) {
	// 总额是 bank2(运费 + 燃油)，**不是** bank2(运费) + bank2(燃油)。
	// 大多数数上两者相同，所以写错了也不容易发现；这里挑一组会分叉的：
	//   运费 1.004、燃油 1.004
	//   先加再舍：bank2(2.008) = 2.01   ← 正确
	//   先舍再加：1.00 + 1.00  = 2.00   ← 差一分
	r := Rule{ChargeMethod: MethodPerPiece, UnitPrice: dec("1.004"), FuelSurchargePct: dec("1")}
	got := Quote(r, QuoteInput{Quantity: dec("1")})
	mustEq(t, got.Amount, dec("2.01"), "总额应先加再舍")
	if got.Amount.Equal(got.FreightAmount.Add(got.FuelSurcharge)) {
		t.Errorf("总额等于两个已舍入分项之和（%s + %s），说明实现改成了先舍再加——"+
			"这会在半分边界上逐笔差一分钱", got.FreightAmount, got.FuelSurcharge)
	}
}

func TestZeroInputsProduceZeroNotPanic(t *testing.T) {
	// 空货量/空规则不能 panic，也不能算出负数
	for _, m := range []string{MethodFlat, MethodPerVolume, MethodPerPiece, MethodPerKm, MethodPerTonKm, MethodTieredWeight} {
		got := Quote(Rule{ChargeMethod: m}, QuoteInput{})
		if got.Amount.IsNegative() {
			t.Errorf("%s 空输入算出负数 %s", m, got.Amount)
		}
	}
}
