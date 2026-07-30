package finance

// 运单费用生成：按合同价算应收（对客户）与应付（对车队/司机）。
//
// 相对旧实现改掉的两件事：
//
//  1. **已进对账单的费用不再删**。旧实现每次重算都 `expenses.filter(source_system="pricing").delete()`，
//     而 StatementLine.expense_record 是 SET_NULL——对一张已出对账单的运单重算，
//     旧费用被删成对账明细的悬空引用，新生成的费用又是一笔"没进过对账单"的记录，
//     下期归集时再收一遍，等于凭空重复计费。现在有闸门：已入账即拒绝重算。
//  2. **删掉主副驾 60/40 拆账**。业务上不存在主副驾，实际形态是「一个订单多辆车、
//     每辆车一个司机」——这在数据模型上已经由「一张订单派生多张运单」表达，
//     每张运单各自计价即可，运单内部不需要再拆。

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Querier 只取读方法，*pgxpool.Pool 与 pgx.Tx 都满足（Exec 的返回类型两者不同，故不纳入）
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func jsonUnmarshal(b []byte, v any) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// ErrAlreadyBilled 该运单的计价费用已进对账单，禁止重算
var ErrAlreadyBilled = errors.New("费用已进入对账单，不能重新生成；如需调整请走对账单调整流程")

type waybillCtx struct {
	ID, No                            string
	CustomerID, CarrierID, DriverID   *string
	CustomerName, CarrierName, Driver string
	RouteName, VehicleType            string
	WeightTon, VolumeCbm, DistanceKm  decimal.Decimal
	Quantity                          int
	CreatedAt                         time.Time
}

// GenerateCosts 生成运单的应收/应付费用；返回各方向生成条数
func GenerateCosts(ctx context.Context, db *pgxpool.Pool, waybillNo string) (map[string]int, error) {
	w, err := loadWaybillCtx(ctx, db, waybillNo)
	if err != nil {
		return nil, err
	}

	// 闸门：任一计价费用已被对账单收录，则整单禁止重算
	var billed bool
	if err := db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM fin_statement_line l
			JOIN fin_expense_record e ON e.id = l.expense_record_id
			WHERE e.waybill_id = $1::uuid AND e.source_system = 'pricing')`, w.ID).Scan(&billed); err != nil {
		return nil, err
	}
	if billed {
		return nil, ErrAlreadyBilled
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"DELETE FROM fin_expense_record WHERE waybill_id=$1::uuid AND source_system='pricing'", w.ID); err != nil {
		return nil, err
	}

	// 费用发生时点统一取建单时间：对账按账期归集、账龄分桶都以此为准
	at := w.CreatedAt
	result := map[string]int{"receivable": 0, "payable": 0}

	// ── 应收：按客户合同计价 ──
	if w.CustomerID != nil {
		n, err := h_generate(ctx, tx, w, "income", "receivable", "customer", *w.CustomerID, w.CustomerName, at)
		if err != nil {
			return nil, err
		}
		result["receivable"] += n
	}

	// ── 应付：按承运商合同计价；自有车队无承运商时应付落到司机 ──
	payeeType, payeeID, payeeRef := "carrier", "", ""
	if w.CarrierID != nil {
		payeeID, payeeRef = *w.CarrierID, w.CarrierName
	} else {
		payeeType, payeeRef = "driver", w.Driver
		if payeeRef == "" {
			payeeRef = "未分配司机池"
		}
	}
	n, err := h_generate(ctx, tx, w, "cost", "payable", payeeType, payeeID, payeeRef, at)
	if err != nil {
		return nil, err
	}
	result["payable"] += n

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// h_generate 匹配合同与规则、计价、落一条费用记录（含合同与规则快照）
func h_generate(ctx context.Context, tx pgx.Tx, w *waybillCtx,
	priceType, direction, payeeType, payeeID, payeeRef string, at time.Time) (int, error) {

	// 合同只在对手方明确时才找：应收看客户，应付看承运商；
	// 自有车队（无承运商）的应付没有对手方合同，直接走全局兜底价。
	var contract *Contract
	var err error
	if payeeType == "customer" || payeeType == "carrier" {
		partyType := payeeType
		if payeeID != "" {
			contract, err = FindContract(ctx, tx, partyType, payeeID, at)
			if err != nil {
				return 0, err
			}
		}
	}
	contractID := ""
	if contract != nil {
		contractID = contract.ID
	}

	custID, carrID := "", ""
	if w.CustomerID != nil {
		custID = *w.CustomerID
	}
	if w.CarrierID != nil {
		carrID = *w.CarrierID
	}
	rule, err := MatchRule(ctx, tx, contractID, priceType, custID, carrID, w.RouteName, w.VehicleType, at)
	if err != nil || rule == nil {
		return 0, err
	}

	in := QuoteInput{WeightTon: w.WeightTon, VolumeCbm: w.VolumeCbm,
		Quantity: decimal.NewFromInt(int64(w.Quantity)), DistanceKm: w.DistanceKm}
	q := Quote(*rule, in)

	snap := ruleSnapshot(*rule, q, in, contract)
	id, _ := uuid.NewV7()
	_, err = tx.Exec(ctx, `
		INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction,
		  expense_item_code, amount, currency, occurred_at, risk_status, source_system, external_id,
		  payee_type, payee_ref, remark, price_source, quote_id, pricing_rule_id, pricing_rule_name,
		  charge_method, matched_condition, input_snapshot, calculation_detail, rule_snapshot,
		  contract_id, contract_no)
		VALUES ($1, now(), now(), $2::uuid, $3, $4, $5::numeric, 'CNY', $6, 'normal', 'pricing', '',
		        $7, $8, '', 'rule', '', $9, $10, $11, $12, $13::jsonb, $14::jsonb, $15::jsonb,
		        $16::uuid, $17)`,
		id.String(), w.ID, direction, rule.ExpenseItemCode, q.Amount.String(), at,
		payeeType, payeeRef, rule.ID, rule.Name, rule.ChargeMethod, snap.matched,
		snap.input, snap.calc, snap.rule, nullIfEmpty(contractID), contractNo(contract))
	if err != nil {
		return 0, err
	}
	return 1, nil
}

func contractNo(c *Contract) string {
	if c == nil {
		return ""
	}
	return c.No
}

type snapshots struct{ matched, input, calc, rule string }

// ruleSnapshot 把命中的合同、规则、匹配条件、计费输入与计算明细固化到费用记录，
// 使得规则/价库日后被改，历史对账仍能完整解释「这笔为什么这么算」。
func ruleSnapshot(r Rule, q QuoteResult, in QuoteInput, c *Contract) snapshots {
	conds := []string{}
	if c != nil {
		conds = append(conds, "合同:"+c.No+"("+contractTypeLabel(c.Type)+")")
	}
	for _, s := range []string{
		labelIf("客户", r.CustomerName), labelIf("承运商", r.CarrierName),
		labelIf("线路", r.RouteName), labelIf("车型", r.VehicleType),
	} {
		if s != "" {
			conds = append(conds, s)
		}
	}
	matched := "通配"
	if len(conds) > 0 {
		matched = join(conds, " / ")
	}
	calc := map[string]any{
		"amount":            q.Amount.InexactFloat64(),
		"charge_method":     q.ChargeMethod,
		"chargeable_weight": q.ChargeableWeight.InexactFloat64(),
		"by_volume":         q.ByVolume,
		"freight_amount":    q.FreightAmount.InexactFloat64(),
		"fuel_surcharge":    q.FuelSurcharge.InexactFloat64(),
	}
	for k, v := range q.Detail {
		calc[k] = v
	}
	inputJSON, _ := json.Marshal(map[string]any{
		"weight_ton": in.WeightTon.InexactFloat64(), "volume_cbm": in.VolumeCbm.InexactFloat64(),
		"quantity": in.Quantity.IntPart(), "distance_km": in.DistanceKm.InexactFloat64(),
	})
	calcJSON, _ := json.Marshal(calc)
	ruleJSON, _ := json.Marshal(map[string]any{
		"base_price": r.BasePrice.InexactFloat64(), "unit_price": r.UnitPrice.InexactFloat64(),
		"min_price": r.MinPrice.InexactFloat64(), "min_charge_qty": r.MinChargeQty.InexactFloat64(),
		"tier_prices": r.TierPrices, "volumetric_factor": r.VolumetricFactor.InexactFloat64(),
		"fuel_surcharge_pct": r.FuelSurchargePct.InexactFloat64(),
		"contract_no":        r.ContractNo,
	})
	return snapshots{matched, string(inputJSON), string(calcJSON), string(ruleJSON)}
}

func contractTypeLabel(t string) string {
	switch t {
	case "long_term":
		return "长期"
	case "short_term":
		return "短期"
	case "temporary":
		return "临时"
	case "agreement":
		return "仅协议"
	}
	return t
}

func labelIf(prefix, v string) string {
	if v == "" {
		return ""
	}
	return prefix + ":" + v
}

func join(ss []string, sep string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func loadWaybillCtx(ctx context.Context, db *pgxpool.Pool, no string) (*waybillCtx, error) {
	w := &waybillCtx{}
	err := db.QueryRow(ctx, `
		SELECT w.id::text, w.waybill_no, w.customer_id::text, w.carrier_id::text, w.driver_id::text,
		       COALESCE(cm.name,''), COALESCE(cr.name,''), COALESCE(dv.name,''),
		       COALESCE(w.route_name,''), COALESCE(v.vehicle_type,''),
		       w.cargo_weight_ton, w.cargo_volume_cbm, COALESCE(r.distance_km, 0),
		       w.cargo_quantity, w.created_at
		FROM ops_waybill w
		LEFT JOIN md_customer cm ON cm.id = w.customer_id
		LEFT JOIN md_carrier  cr ON cr.id = w.carrier_id
		LEFT JOIN md_driver   dv ON dv.id = w.driver_id
		LEFT JOIN md_vehicle  v  ON v.id  = w.vehicle_id
		LEFT JOIN md_route    r  ON r.id  = w.planned_route_id
		WHERE w.waybill_no = $1`, no).
		Scan(&w.ID, &w.No, &w.CustomerID, &w.CarrierID, &w.DriverID,
			&w.CustomerName, &w.CarrierName, &w.Driver, &w.RouteName, &w.VehicleType,
			&w.WeightTon, &w.VolumeCbm, &w.DistanceKm, &w.Quantity, &w.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, pgx.ErrNoRows
	}
	return w, err
}
