"""费用归集与事件发射。"""

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
