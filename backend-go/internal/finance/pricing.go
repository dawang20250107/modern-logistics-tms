package finance

// 计价引擎：合同 → 规则 → 报价。
//
// 与旧实现的关键差别：价格挂在**有起止期的运输合同**下，不再是漂在全局的规则表。
// 于是三件事同时成立：
//   1. 每笔费用能答出「按哪份合同哪个价算的」（contract_id/contract_no 快照）；
//   2. 改一份合同的价不会波及别的客户；
//   3. 调价不会把历史运单重算成新价——匹配一律按**费用发生时点**筛生效合同与规则。
//
// 无合同（临时单/仅口头协议）时回落到 contract_id IS NULL 的全局兜底价，
// 保证"没签合同也能报价"这条真实业务路径不断。

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// 计费方式
const (
	MethodTieredWeight = "tiered_weight"
	MethodFlat         = "flat"
	MethodPerVolume    = "per_volume"
	MethodPerPiece     = "per_piece"
	MethodPerKm        = "per_km"
	MethodPerTonKm     = "per_ton_km"
)

// VolumetricFactorTonPerCbm 抛重系数：1 立方米 ≈ 0.333 吨（抛比约 1:3，泡货按体积重计费）
var VolumetricFactorTonPerCbm = decimal.RequireFromString("0.333")

// Contract 生效中的运输合同
type Contract struct {
	ID, No, Name, Type string
	PartyType, PartyID string
	SettlementType     string
	CreditDays         int
	BillingDay         int
}

// Rule 计价规则
type Rule struct {
	ID, Name, PriceType, ChargeMethod, ExpenseItemCode string
	ContractID, ContractNo                             string
	CustomerName, CarrierName, RouteName, VehicleType  string
	BasePrice, MinPrice, UnitPrice, MinChargeQty       decimal.Decimal
	VolumetricFactor, FuelSurchargePct                 decimal.Decimal
	TierPrices                                         []map[string]any
}

// QuoteInput 计价输入
type QuoteInput struct {
	WeightTon, VolumeCbm, Quantity, DistanceKm decimal.Decimal
}

// QuoteResult 报价结果（Detail 为随计费方式而异的中间量）
type QuoteResult struct {
	Amount           decimal.Decimal
	ChargeMethod     string
	ChargeableWeight decimal.Decimal
	ByVolume         bool
	FreightAmount    decimal.Decimal
	FuelSurcharge    decimal.Decimal
	Detail           map[string]any
}

// bank2 复刻 Python 对 Decimal 的 round(x, 2)：银行家舍入（ROUND_HALF_EVEN）。
// 用普通四舍五入会在 .005 这类边界上与既有账目差一分钱，对账时是要人命的差异。
func bank2(d decimal.Decimal) decimal.Decimal { return d.RoundBank(2) }

// ChargeableWeight 计费重：取实际重量与体积重（抛重）的较大值，避免泡货按净重少收
func ChargeableWeight(weightTon, volumeCbm, factor decimal.Decimal) decimal.Decimal {
	vol := volumeCbm.Mul(factor)
	if vol.GreaterThan(weightTon) {
		return vol
	}
	return weightTon
}

// Quote 按计费方式计算运费：
// 先算计费重 → 按方式算基础运费（起步价 + 计量部分）→ 取 min_price 下限 → 叠加燃油附加费。
func Quote(r Rule, in QuoteInput) QuoteResult {
	factor := r.VolumetricFactor
	if factor.IsZero() {
		factor = VolumetricFactorTonPerCbm
	}
	volWeight := in.VolumeCbm.Mul(factor)
	chargeable := ChargeableWeight(in.WeightTon, in.VolumeCbm, factor)
	floor := r.MinChargeQty
	detail := map[string]any{}

	var freight decimal.Decimal
	switch r.ChargeMethod {
	case MethodFlat:
		freight = r.BasePrice
	case MethodPerVolume:
		billable := maxDec(in.VolumeCbm, floor)
		detail["billable_volume"] = billable.Round(3).InexactFloat64()
		freight = r.BasePrice.Add(r.UnitPrice.Mul(billable))
	case MethodPerPiece:
		billable := maxDec(in.Quantity, floor)
		detail["billable_pieces"] = billable.Round(3).InexactFloat64()
		freight = r.BasePrice.Add(r.UnitPrice.Mul(billable))
	case MethodPerKm:
		detail["distance_km"] = in.DistanceKm.Round(2).InexactFloat64()
		freight = r.BasePrice.Add(r.UnitPrice.Mul(in.DistanceKm))
	case MethodPerTonKm:
		billable := maxDec(chargeable, floor)
		detail["ton_km"] = billable.Mul(in.DistanceKm).Round(2).InexactFloat64()
		freight = r.BasePrice.Add(r.UnitPrice.Mul(billable).Mul(in.DistanceKm))
	default: // tiered_weight
		billable := maxDec(chargeable, floor)
		pricePerTon := decimal.Zero
		for _, tier := range r.TierPrices {
			minT := decFrom(tier["min_ton"], decimal.Zero)
			maxT := decFrom(tier["max_ton"], decimal.NewFromInt(999999))
			if minT.LessThanOrEqual(billable) && billable.LessThanOrEqual(maxT) {
				pricePerTon = decFrom(tier["price"], decimal.Zero)
				break
			}
		}
		freight = r.BasePrice.Add(pricePerTon.Mul(billable))
	}

	freight = maxDec(freight, r.MinPrice)
	fuel := freight.Mul(r.FuelSurchargePct)
	return QuoteResult{
		Amount:           bank2(freight.Add(fuel)),
		ChargeMethod:     r.ChargeMethod,
		ChargeableWeight: chargeable.RoundBank(3),
		ByVolume:         volWeight.GreaterThan(in.WeightTon),
		FreightAmount:    bank2(freight),
		FuelSurcharge:    bank2(fuel),
		Detail:           detail,
	}
}

func maxDec(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}
	return b
}

func decFrom(v any, def decimal.Decimal) decimal.Decimal {
	switch t := v.(type) {
	case float64:
		return decimal.NewFromFloat(t)
	case string:
		if d, err := decimal.NewFromString(t); err == nil {
			return d
		}
	case int:
		return decimal.NewFromInt(int64(t))
	}
	return def
}

// contractTypeRank 多份合同同时生效时的取价优先级。
// 临时合同与仅协议就是用来压住长期框架价的（这单特批一个价），所以排在前面；
// 同档再按生效日期倒序取最新签的那份。
const contractTypeRank = `CASE c.contract_type
    WHEN 'temporary' THEN 0 WHEN 'agreement' THEN 1
    WHEN 'short_term' THEN 2 ELSE 3 END`

// FindContract 按对手方与费用发生时点找当时生效的合同；无则返回 nil
func FindContract(ctx context.Context, q Querier, partyType, partyID string, at time.Time) (*Contract, error) {
	if partyID == "" {
		return nil, nil
	}
	c := &Contract{}
	err := q.QueryRow(ctx, `
		SELECT c.id::text, c.contract_no, c.name, c.contract_type, c.party_type, c.party_id::text,
		       c.settlement_type, c.credit_days, c.billing_day
		FROM fin_contract c
		WHERE NOT c.is_deleted AND c.status='active'
		  AND c.party_type=$1 AND c.party_id=$2::uuid
		  AND c.effective_from <= $3::date
		  AND (c.effective_to IS NULL OR c.effective_to >= $3::date)
		ORDER BY `+contractTypeRank+`, c.effective_from DESC, c.id
		LIMIT 1`, partyType, partyID, at).
		Scan(&c.ID, &c.No, &c.Name, &c.Type, &c.PartyType, &c.PartyID,
			&c.SettlementType, &c.CreditDays, &c.BillingDay)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// MatchRule 取该合同下（或无合同时的全局兜底）最优先的一条生效规则。
// customerID/carrierID 仅用于无合同回落时的匹配；有合同时对手方已由合同锁定。
func MatchRule(ctx context.Context, q Querier, contractID, priceType, customerID, carrierID,
	routeName, vehicleType string, at time.Time) (*Rule, error) {
	scope := "p.contract_id IS NULL"
	args := []any{priceType, at, routeName, vehicleType}
	if contractID != "" {
		scope = "p.contract_id = $5::uuid"
		args = append(args, contractID)
	} else {
		// 无合同兜底：沿用旧的对手方通配匹配（规则字段为空即通配）
		scope += ` AND (p.customer_id IS NULL OR p.customer_id::text = COALESCE($5,''))
		           AND (p.carrier_id IS NULL OR p.carrier_id::text = COALESCE($6,''))`
		args = append(args, nullIfEmpty(customerID), nullIfEmpty(carrierID))
	}
	r := &Rule{}
	var tiers []byte
	err := q.QueryRow(ctx, `
		SELECT p.id::text, p.name, p.price_type, p.charge_method, p.expense_item_code,
		       COALESCE(p.contract_id::text,''), COALESCE(c.contract_no,''),
		       COALESCE(cm.name,''), COALESCE(cr.name,''), p.route_name, p.vehicle_type,
		       p.base_price, p.min_price, p.unit_price, p.min_charge_qty,
		       p.volumetric_factor, p.fuel_surcharge_pct, p.tier_prices
		FROM fin_pricing_rule p
		LEFT JOIN fin_contract c ON c.id = p.contract_id
		LEFT JOIN md_customer cm ON cm.id = p.customer_id
		LEFT JOIN md_carrier cr ON cr.id = p.carrier_id
		WHERE p.is_active AND p.price_type = $1
		  AND (p.effective_from IS NULL OR p.effective_from <= $2::date)
		  AND (p.effective_to   IS NULL OR p.effective_to   >= $2::date)
		  AND (p.route_name = '' OR p.route_name = $3)
		  AND (p.vehicle_type = '' OR p.vehicle_type = $4)
		  AND `+scope+`
		ORDER BY p.priority DESC, p.name, p.id
		LIMIT 1`, args...).
		Scan(&r.ID, &r.Name, &r.PriceType, &r.ChargeMethod, &r.ExpenseItemCode,
			&r.ContractID, &r.ContractNo, &r.CustomerName, &r.CarrierName, &r.RouteName, &r.VehicleType,
			&r.BasePrice, &r.MinPrice, &r.UnitPrice, &r.MinChargeQty,
			&r.VolumetricFactor, &r.FuelSurchargePct, &tiers)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = jsonUnmarshal(tiers, &r.TierPrices)
	return r, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
