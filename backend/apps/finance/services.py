"""费用归集与事件发射。"""

from decimal import Decimal

from .models import ExpenseRecord, PricingRule, Webhook, WebhookDelivery


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


def estimate_order_quote(*, customer_id=None, route_name="", weight_ton=0) -> dict:
    """录单自动报价：按收入计价规则对订单货量估价，返回最优（最高优先级）匹配。

    匹配条件：客户/线路通配；命中多条按 priority 取最高。返回 {amount, rule_name, matched}。
    """
    best = None
    for rule in PricingRule.objects.filter(
        is_active=True, price_type=PricingRule.PRICE_TYPE_INCOME
    ).order_by("-priority"):
        if rule.customer_id and rule.customer_id != customer_id:
            continue
        if rule.route_name and rule.route_name != route_name:
            continue
        best = rule
        break
    if best is None:
        return {"amount": 0.0, "rule_name": "", "matched": False}
    return {"amount": float(best.quote(weight_ton)), "rule_name": best.name, "matched": True}


def generate_costs(waybill) -> dict:
    """按报价规则生成运单应收/应付（替换既往规则自动生成的记录）。"""
    waybill.expenses.filter(source_system="pricing").delete()
    result = {"receivable": 0, "payable": 0}
    weight = waybill.cargo_weight_ton

    income = _match_rules(waybill, PricingRule.PRICE_TYPE_INCOME)
    if income:
        rule = income[0]
        ExpenseRecord.objects.create(
            waybill=waybill,
            direction=ExpenseRecord.DIRECTION_RECEIVABLE,
            expense_item_code=rule.expense_item_code,
            amount=rule.quote(weight),
            source_system="pricing",
        )
        result["receivable"] = 1

    cost = _match_rules(waybill, PricingRule.PRICE_TYPE_COST)
    if cost:
        rule = cost[0]
        ExpenseRecord.objects.create(
            waybill=waybill,
            direction=ExpenseRecord.DIRECTION_PAYABLE,
            expense_item_code=rule.expense_item_code,
            amount=rule.quote(weight),
            source_system="pricing",
        )
        result["payable"] = 1

    emit_event("cost.generated", {"waybill_no": waybill.waybill_no, "generated": result})
    return result


def estimate_costs(waybill) -> dict:
    """按报价规则预估收入/成本/毛利（不落库），供调度建议使用。"""
    weight = waybill.cargo_weight_ton
    income = _match_rules(waybill, PricingRule.PRICE_TYPE_INCOME)
    cost = _match_rules(waybill, PricingRule.PRICE_TYPE_COST)
    income_amt = float(income[0].quote(weight)) if income else 0.0
    cost_amt = float(cost[0].quote(weight)) if cost else 0.0
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
    import random

    from django.utils import timezone

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
        statement_no=f"ST{timezone.now():%Y%m%d%H%M%S}{random.randint(100, 999)}",
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
            waybill_no=r.waybill.waybill_no if r.waybill else "",
            expense_item_code=r.expense_item_code,
            amount=r.amount,
            occurred_at=r.occurred_at,
        )
        for r in records
    ])
    return statement


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
