"""费用归集与事件发射。"""

from decimal import Decimal

from .models import ExpenseRecord, PricingRule, Webhook, WebhookDelivery


def gen_statement_no() -> str:
    """对账单号 ST + 日期 + 原子日序号，保证并发/同秒唯一（复用 ops 单号计数器）。"""
    from django.utils import timezone

    from apps.ops.numbering import next_sequence

    day = timezone.now().strftime("%Y%m%d")
    return f"ST{day}{next_sequence(f'statement:{day}'):06d}"


def _match_rules(waybill, price_type) -> list[PricingRule]:
    vehicle_type = waybill.vehicle.vehicle_type if waybill.vehicle else ""
    matched = []
    for rule in PricingRule.objects.filter(is_active=True, price_type=price_type).order_by("-priority"):
        if rule.customer_id and rule.customer_id != waybill.customer_id:
            continue
        if rule.carrier_id and rule.carrier_id != waybill.carrier_id:
            continue
        if rule.route_name and rule.route_name != waybill.route_name:
            continue
        if rule.vehicle_type and rule.vehicle_type != vehicle_type:
            continue
        matched.append(rule)
    return matched


# 抛重系数：1 立方米 ≈ 0.333 吨（抛比约 1:3，泡货按体积重计费）
VOLUMETRIC_FACTOR_TON_PER_CBM = Decimal("0.333")


def chargeable_weight(weight_ton, volume_cbm, factor=VOLUMETRIC_FACTOR_TON_PER_CBM) -> Decimal:
    """计费重量：取实际重量与体积重（抛重）的较大值，避免泡货按净重少收。"""
    actual = Decimal(str(weight_ton or 0))
    volumetric = Decimal(str(volume_cbm or 0)) * factor
    return max(actual, volumetric)


def estimate_order_quote(*, customer_id=None, route_name="", weight_ton=0, volume_cbm=0,
                         quantity=0, distance_km=0) -> dict:
    """录单自动报价：按收入计价规则对订单估价，返回最优（最高优先级）匹配。

    支持整车/阶梯重/按方/按件/按公里/吨公里六种计费方式（见 PricingRule.quote）。
    匹配条件：客户/线路通配，按 priority 取最高。
    """
    cust = str(customer_id) if customer_id else ""
    cw = chargeable_weight(weight_ton, volume_cbm)
    volumetric = Decimal(str(volume_cbm or 0)) * VOLUMETRIC_FACTOR_TON_PER_CBM
    base = {
        "actual_weight": float(weight_ton or 0),
        "volumetric_weight": round(float(volumetric), 3),
        "chargeable_weight": float(cw),
        "by_volume": volumetric > Decimal(str(weight_ton or 0)),
    }
    best = None
    for rule in PricingRule.objects.filter(
        is_active=True, price_type=PricingRule.PRICE_TYPE_INCOME
    ).order_by("-priority"):
        if rule.customer_id and str(rule.customer_id) != cust:
            continue
        if rule.route_name and rule.route_name != route_name:
            continue
        best = rule
        break
    if best is None:
        return {"amount": 0.0, "rule_name": "", "matched": False, "charge_method": "", **base}
    quote_result = best.quote(weight_ton, volume_cbm, quantity=quantity, distance_km=distance_km)
    return {
        "amount": float(quote_result["amount"]), "rule_name": best.name, "matched": True,
        "charge_method": best.charge_method, "charge_method_label": best.get_charge_method_display(),
        **base,
    }


def _rule_snapshot(rule, quote_result, *, weight, volume, qty, distance) -> dict:
    """费用规则快照：把当时命中的规则、匹配条件、计费输入与计算明细固化到费用记录，
    使得后续即便规则/价库被修改，历史对账仍可完整解释「这笔为什么这么算」。"""
    def _num(v):
        return float(v) if isinstance(v, (int, float, Decimal)) else v

    conditions = [
        f"客户:{rule.customer.name}" if rule.customer_id else "",
        f"承运商:{rule.carrier.name}" if rule.carrier_id else "",
        f"线路:{rule.route_name}" if rule.route_name else "",
        f"车型:{rule.vehicle_type}" if rule.vehicle_type else "",
    ]
    return {
        "price_source": "rule",
        "pricing_rule_id": str(rule.id),
        "pricing_rule_name": rule.name,
        "charge_method": rule.charge_method,
        "matched_condition": " / ".join(c for c in conditions if c) or "通配",
        "input_snapshot": {
            "weight_ton": float(weight or 0), "volume_cbm": float(volume or 0),
            "quantity": int(qty or 0), "distance_km": float(distance or 0),
        },
        "calculation_detail": {k: _num(v) for k, v in quote_result.items()},
        "rule_snapshot": {
            "base_price": float(rule.base_price), "unit_price": float(rule.unit_price),
            "min_price": float(rule.min_price), "min_charge_qty": float(rule.min_charge_qty),
            "tier_prices": rule.tier_prices, "volumetric_factor": float(rule.volumetric_factor),
            "fuel_surcharge_pct": float(rule.fuel_surcharge_pct),
        },
    }


def generate_costs(waybill) -> dict:
    """业财结算核心引擎：按报价规则生成运单应收/应付，并支持主副驾智能运费切分（Payee-Split）。"""
    waybill.expenses.filter(source_system="pricing").delete()
    result = {"receivable": 0, "payable": 0}
    weight = waybill.cargo_weight_ton
    volume = waybill.cargo_volume_cbm
    qty = waybill.cargo_quantity
    distance = waybill.planned_route.distance_km if waybill.planned_route_id else 0

    # === AR (应收账款) 逻辑 ===
    income = _match_rules(waybill, PricingRule.PRICE_TYPE_INCOME)
    if income:
        rule = income[0]
        quote_result = rule.quote(weight, volume, quantity=qty, distance_km=distance)
        ExpenseRecord.objects.create(
            waybill=waybill,
            direction=ExpenseRecord.DIRECTION_RECEIVABLE,
            expense_item_code=rule.expense_item_code,
            amount=quote_result["amount"],
            payee_type="customer",
            payee_ref=waybill.customer.name if waybill.customer_id else "",
            source_system="pricing",
            **_rule_snapshot(rule, quote_result, weight=weight, volume=volume, qty=qty, distance=distance),
        )
        result["receivable"] = 1

    # === AP (应付账款) 逻辑与智能切分引擎 ===
    cost = _match_rules(waybill, PricingRule.PRICE_TYPE_COST)
    if cost:
        rule = cost[0]
        quote_result = rule.quote(weight, volume, quantity=qty, distance_km=distance)
        total_ap_amount = quote_result["amount"]
        
        # 模式一：外部承运商 / 专线老板 (合并池化结算，不直接跟底层司机拆分运费)
        if waybill.carrier_id:
            ExpenseRecord.objects.create(
                waybill=waybill,
                direction=ExpenseRecord.DIRECTION_PAYABLE,
                expense_item_code=rule.expense_item_code,
                amount=total_ap_amount,
                payee_type="carrier",
                payee_ref=waybill.carrier.name,
                source_system="pricing",
                **_rule_snapshot(rule, quote_result, weight=weight, volume=volume, qty=qty, distance=distance),
            )
            result["payable"] += 1
            
        # 模式二：直管 / 自营多司机网络 (主副驾/多节点接力智能拆账)
        else:
            # 取出本次排班绑定的所有司机 (区分主副驾)
            drivers = list(waybill.driver_assignments.select_related("driver").all())
            
            if len(drivers) == 0:
                # 兜底：无司机挂靠
                ExpenseRecord.objects.create(
                    waybill=waybill,
                    direction=ExpenseRecord.DIRECTION_PAYABLE,
                    expense_item_code=rule.expense_item_code,
                    amount=total_ap_amount,
                    payee_type="driver",
                    payee_ref="未分配司机池",
                    source_system="pricing",
                )
                result["payable"] += 1
                
            elif len(drivers) == 1:
                # 单司机直跑，全额100%划拨结算
                ExpenseRecord.objects.create(
                    waybill=waybill,
                    direction=ExpenseRecord.DIRECTION_PAYABLE,
                    expense_item_code=rule.expense_item_code,
                    amount=total_ap_amount,
                    payee_type="driver",
                    payee_ref=drivers[0].driver.name,
                    source_system="pricing",
                )
                result["payable"] += 1
                
            else:
                # 顶级实战：双驾/多驾切分。业内默认主驾拿 60%，副驾平分剩余 40%
                main_driver_assignment = next((d for d in drivers if d.role == "main"), drivers[0])
                co_driver_assignments = [d for d in drivers if d.id != main_driver_assignment.id]
                
                # 拆分数学计算
                main_split = round(total_ap_amount * Decimal("0.60"), 2)
                co_split_total = total_ap_amount - main_split
                co_split_per = round(co_split_total / len(co_driver_assignments), 2)
                
                # 1. 主驾账单落库
                ExpenseRecord.objects.create(
                    waybill=waybill,
                    direction=ExpenseRecord.DIRECTION_PAYABLE,
                    expense_item_code=rule.expense_item_code,
                    amount=main_split,
                    payee_type="driver",
                    payee_ref=f"{main_driver_assignment.driver.name} (主驾拆账60%)",
                    source_system="pricing",
                )
                result["payable"] += 1
                
                # 2. 副驾群账单落库
                for i, co_assignment in enumerate(co_driver_assignments):
                    # 最后一个副驾拿余数平账防溢出
                    amt = co_split_per if i < len(co_driver_assignments) - 1 else (co_split_total - co_split_per * (len(co_driver_assignments) - 1))
                    ExpenseRecord.objects.create(
                        waybill=waybill,
                        direction=ExpenseRecord.DIRECTION_PAYABLE,
                        expense_item_code=rule.expense_item_code,
                        amount=amt,
                        payee_type="driver",
                        payee_ref=f"{co_assignment.driver.name} (副驾拆账)",
                        source_system="pricing",
                    )
                    result["payable"] += 1

    emit_event("cost.generated", {"waybill_no": waybill.waybill_no, "generated": result})
    return result


def estimate_costs(waybill) -> dict:
    """按报价规则预估收入/成本/毛利（不落库），供调度建议使用。"""
    weight = waybill.cargo_weight_ton
    volume = waybill.cargo_volume_cbm
    qty = waybill.cargo_quantity
    distance = waybill.planned_route.distance_km if waybill.planned_route_id else 0
    income = _match_rules(waybill, PricingRule.PRICE_TYPE_INCOME)
    cost = _match_rules(waybill, PricingRule.PRICE_TYPE_COST)
    income_amt = float(income[0].quote(weight, volume, quantity=qty, distance_km=distance)["amount"]) if income else 0.0
    cost_amt = float(cost[0].quote(weight, volume, quantity=qty, distance_km=distance)["amount"]) if cost else 0.0
    return {"income": income_amt, "cost": cost_amt, "gross": round(income_amt - cost_amt, 2)}


def emit_event(event_type: str, payload: dict) -> int:
    """向订阅的 Webhook 异步投递事件。返回投递数。"""
    from .tasks import deliver_webhook

    count = 0
    for webhook in Webhook.objects.filter(is_active=True):
        if not webhook.subscribes(event_type):
            continue
        delivery = WebhookDelivery.objects.create(webhook=webhook, event_type=event_type, payload=payload)
        deliver_webhook.delay(str(delivery.id))
        count += 1
    return count


def generate_statement(*, direction, counterparty_type, counterparty_id, start, end, external_total=0):
    """按客户(应收)/承运商(应付)在账期内归集费用，生成对账单与明细。"""


    from .models import ExpenseRecord, Statement, StatementLine

    field = "waybill__customer_id" if counterparty_type == Statement.CP_CUSTOMER else "waybill__carrier_id"
    qs = (
        ExpenseRecord.objects.select_related("waybill")
        .filter(direction=direction, occurred_at__date__gte=start, occurred_at__date__lte=end)
        .filter(**{field: counterparty_id})
        .order_by("occurred_at")
    )
    records = list(qs)
    total = sum((r.amount for r in records), Decimal("0"))

    name = _counterparty_name(counterparty_type, counterparty_id)
    statement = Statement.objects.create(
        statement_no=gen_statement_no(),
        direction=direction,
        counterparty_type=counterparty_type,
        counterparty_id=str(counterparty_id),
        counterparty_name=name,
        period_start=start,
        period_end=end,
        total_amount=total,
        item_count=len(records),
        external_total=external_total or 0,
    )
    StatementLine.objects.bulk_create([
        StatementLine(
            statement=statement,
            expense_record=r,
            waybill_no=r.waybill.waybill_no if r.waybill else "",
            expense_item_code=r.expense_item_code,
            amount=r.amount,
            occurred_at=r.occurred_at,
        )
        for r in records
    ])
    return statement


def generate_statement_for_batch(batch, *, external_total=0):
    """按派车批次一键生成承运商应付对账单：归集该批次各运单的应付流水为一张对账单。

    批次 = 一次委托同一承运商的商务归集，天然对应一个承运商应付分组；
    对账口径直接取派单时落下的议定应付快照（price_source=batch），可解释、可追溯。
    幂等：批次已生成对账单则直接返回原单，避免重复归集。
    """


    from apps.core.exceptions import AppError

    from .models import ExpenseRecord, Statement, StatementLine

    if batch.carrier_id is None:
        raise AppError("BATCH_NO_CARRIER", "网货平台批次无承运商，暂不支持一键对账。", status=400)
    if batch.statement_no:
        existing = Statement.objects.filter(statement_no=batch.statement_no).first()
        if existing:
            return existing

    records = list(
        ExpenseRecord.objects.select_related("waybill")
        .filter(direction=Statement.DIRECTION_PAYABLE, waybill__batch_id=batch.id)
        .order_by("occurred_at")
    )
    total = sum((r.amount for r in records), Decimal("0"))
    dates = [r.occurred_at.date() for r in records if r.occurred_at] or [batch.created_at.date()]

    statement = Statement.objects.create(
        statement_no=gen_statement_no(),
        direction=Statement.DIRECTION_PAYABLE,
        counterparty_type=Statement.CP_CARRIER,
        counterparty_id=str(batch.carrier_id),
        counterparty_name=batch.carrier.name if batch.carrier else "",
        period_start=min(dates),
        period_end=max(dates),
        total_amount=total,
        item_count=len(records),
        external_total=external_total or 0,
    )
    StatementLine.objects.bulk_create([
        StatementLine(
            statement=statement,
            expense_record=r,
            waybill_no=r.waybill.waybill_no if r.waybill else "",
            expense_item_code=r.expense_item_code,
            amount=r.amount,
            occurred_at=r.occurred_at,
        )
        for r in records
    ])
    batch.statement_no = statement.statement_no
    batch.save(update_fields=["statement_no", "updated_at"])
    return statement


# 异常审计阈值：超出同科目历史均值 50% 且绝对超出 ≥ 50 元才标红，避免小额噪声误报
ANOMALY_RATIO = Decimal("1.5")
ANOMALY_FLOOR = Decimal("50")
ANOMALY_MIN_SAMPLES = 3


def audit_statement(statement) -> dict:
    """AI 异常审计：按「同费用科目 + 同账单方向」的历史均值，检出本单子流水中的过高费用。

    非模拟——对每行查询该科目（不含自身）的历史 ExpenseRecord 均值作为基线，
    超出基线 50% 且绝对差额不低于 50 元才标红；样本不足 3 笔不下结论。
    审计结果回写 StatementLine（供前端展示）与关联 ExpenseRecord.risk_status
    （供 AI 工作台 expense_risk_check 等下游复用同一份风险信号）。
    """
    from django.db.models import Avg, Count
    from django.utils import timezone

    from .models import ExpenseRecord, StatementLine

    lines = list(statement.lines.select_related("expense_record"))
    anomaly_count = 0
    for line in lines:
        stats = (
            ExpenseRecord.objects.filter(
                direction=statement.direction, expense_item_code=line.expense_item_code
            )
            .exclude(id=line.expense_record_id)
            .aggregate(avg=Avg("amount"), n=Count("id"))
        )
        baseline, n = stats["avg"], stats["n"]
        if baseline is None or n < ANOMALY_MIN_SAMPLES:
            line.baseline_avg = None
            line.deviation_pct = None
            line.is_anomaly = False
            continue
        line.baseline_avg = baseline
        line.deviation_pct = round((line.amount - baseline) / baseline * 100, 1) if baseline else None
        line.is_anomaly = bool(
            line.amount > baseline * ANOMALY_RATIO and (line.amount - baseline) >= ANOMALY_FLOOR
        )
        if line.is_anomaly:
            anomaly_count += 1
        if line.expense_record_id:
            new_status = "high_deviation" if line.is_anomaly else "normal"
            if line.expense_record.risk_status != new_status:
                ExpenseRecord.objects.filter(id=line.expense_record_id).update(risk_status=new_status)

    if lines:
        StatementLine.objects.bulk_update(lines, ["is_anomaly", "baseline_avg", "deviation_pct"])
    statement.audited_at = timezone.now()
    statement.save(update_fields=["audited_at", "updated_at"])
    return {"total_lines": len(lines), "anomaly_count": anomaly_count, "audited_at": statement.audited_at}


def _counterparty_name(counterparty_type, counterparty_id) -> str:
    from apps.masterdata.models import Carrier, Customer

    from .models import Statement

    model = Customer if counterparty_type == Statement.CP_CUSTOMER else Carrier
    obj = model.objects.filter(id=counterparty_id).first()
    return obj.name if obj else ""


def confirm_statement(statement, *, operator=None):
    from django.utils import timezone

    from .models import Statement

    if statement.status != Statement.STATUS_DRAFT:
        from apps.core.exceptions import AppError

        raise AppError("INVALID_STATEMENT_STATUS", "仅草稿对账单可确认。", status=409)
    statement.status = Statement.STATUS_CONFIRMED
    statement.confirmed_by = operator if operator and operator.is_authenticated else None
    statement.confirmed_at = timezone.now()
    statement.save(update_fields=["status", "confirmed_by", "confirmed_at", "updated_at"])
    return statement


def aging_report(direction: str) -> dict:
    """应收(客户)/应付(承运商)账龄：按对手方 + 账龄桶(0-30/31-60/61-90/90+)汇总。"""
    from django.utils import timezone

    from apps.masterdata.models import Carrier, Customer

    is_receivable = direction == ExpenseRecord.DIRECTION_RECEIVABLE
    cp_field = "waybill__customer_id" if is_receivable else "waybill__carrier_id"
    today = timezone.localdate()

    rows: dict = {}
    qs = ExpenseRecord.objects.filter(direction=direction).values(cp_field, "occurred_at", "amount")
    for rec in qs:
        cp_id = rec[cp_field]
        if cp_id is None:
            continue
        occurred = rec["occurred_at"]
        age = (today - occurred.date()).days if occurred else 0
        bucket = "b0_30" if age <= 30 else "b31_60" if age <= 60 else "b61_90" if age <= 90 else "b90"
        row = rows.setdefault(cp_id, {"b0_30": Decimal("0"), "b31_60": Decimal("0"), "b61_90": Decimal("0"), "b90": Decimal("0")})
        row[bucket] += rec["amount"]

    model = Customer if is_receivable else Carrier
    names = {str(c.id): c.name for c in model.objects.filter(id__in=list(rows.keys()))}
    result = []
    totals = {"b0_30": 0.0, "b31_60": 0.0, "b61_90": 0.0, "b90": 0.0, "total": 0.0}
    for cp_id, row in rows.items():
        total = sum(row.values())
        item = {
            "counterparty_id": str(cp_id),
            "counterparty_name": names.get(str(cp_id), ""),
            **{k: float(v) for k, v in row.items()},
            "total": float(total),
        }
        for k in ("b0_30", "b31_60", "b61_90", "b90"):
            totals[k] += float(row[k])
        totals["total"] += float(total)
        result.append(item)
    result.sort(key=lambda x: x["total"], reverse=True)
    return {"direction": direction, "rows": result, "totals": totals}


def waybill_finance_card(waybill) -> dict:
    """单票财务卡：客户报价 / 承运商报价 / 平台服务费 / 预计·实际毛利 /
    是否可对账（回单满足付款条件、无未决异常扣款）。

    - 应收（客户报价）= 该运单应收费用合计
    - 应付（承运商报价）= 该运单应付费用合计
    - 其他（平台服务费/杂费）= 外部费用合计
    - 毛利 = 应收 − 应付 − 其他
    - 异常扣款 = 该运单未结异常的责任金额
    - 可对账 = 回单已回收/核销 且 无未决异常
    """
    from django.db.models import Sum

    from apps.ops.models import ExceptionRecord

    def _sum(direction):
        return float(
            ExpenseRecord.objects.filter(waybill=waybill, direction=direction)
            .aggregate(s=Sum("amount")).get("s") or 0
        )

    receivable = _sum("receivable")
    payable = _sum("payable")
    other = _sum("external")
    gross = round(receivable - payable - other, 2)

    open_exc = ExceptionRecord.objects.filter(waybill=waybill).exclude(status="resolved")
    exception_deduction = float(open_exc.aggregate(s=Sum("amount")).get("s") or 0)
    has_open_exception = open_exc.exists()

    receipt_ok = waybill.receipt_status in ("returned", "audited")
    reconcilable = receipt_ok and not has_open_exception

    blockers = []
    if not receipt_ok:
        blockers.append("回单未回收")
    if has_open_exception:
        blockers.append("存在未决异常")

    return {
        "waybill_no": waybill.waybill_no,
        "customer_name": waybill.customer.name if waybill.customer_id else "散客",
        "carrier_name": waybill.carrier.name if waybill.carrier_id else "",
        "receivable": receivable,
        "payable": payable,
        "other_fee": other,
        "gross_margin": gross,
        "margin_pct": round(gross / receivable, 3) if receivable else None,
        "exception_deduction": exception_deduction,
        "receipt_ok": receipt_ok,
        "reconcilable": reconcilable,
        "blockers": blockers,
    }
