"""派单闭环：类型校验 + 承运状态 + 议定应付金额快照 + 事件 + 费用规则快照。"""

from decimal import Decimal

import pytest

from apps.core.exceptions import AppError
from apps.finance.models import ExpenseRecord, PricingRule
from apps.masterdata.models import Carrier, Vehicle
from apps.ops.models import Order, Waybill


def _pooled(weight=6):
    from apps.ops.intake import create_order_from_intake, pool_order

    o = create_order_from_intake(fields={"origin": "上海", "destination": "杭州", "cargo_weight_ton": weight})
    o.status = Order.STATUS_CONFIRMED
    o.save()
    pool_order(o)
    return o


@pytest.mark.django_db
def test_third_party_requires_carrier():
    from apps.ops.order_dispatch import dispatch_order

    with pytest.raises(AppError) as ex:
        dispatch_order(_pooled(), dispatch_type="third_party")
    assert ex.value.code == "CARRIER_REQUIRED"


@pytest.mark.django_db
def test_platform_requires_platform_name():
    from apps.ops.order_dispatch import dispatch_order

    with pytest.raises(AppError) as ex:
        dispatch_order(_pooled(), dispatch_type="platform")
    assert ex.value.code == "PLATFORM_REQUIRED"


@pytest.mark.django_db
def test_own_vehicle_requires_vehicle():
    from apps.ops.order_dispatch import dispatch_order

    with pytest.raises(AppError) as ex:
        dispatch_order(_pooled(), dispatch_type="own_vehicle")
    assert ex.value.code == "VEHICLE_REQUIRED"


@pytest.mark.django_db
def test_third_party_dispatch_snapshots_payable_and_events():
    from apps.ops.order_dispatch import dispatch_order

    carrier = Carrier.objects.create(code="C1", name="华东顺捷")
    wb = dispatch_order(
        _pooled(), dispatch_type="third_party", carrier=carrier,
        agreed_payable_amount=1850, price_source="recommended", quote_id="lane-1",
        price_remark="综合推荐承运商",
    )
    assert wb.dispatch_status == "pending_accept"  # 承运商待接单

    exp = ExpenseRecord.objects.get(waybill=wb, direction="payable")
    assert exp.amount == Decimal("1850")
    assert exp.price_source == "recommended"
    assert exp.payee_type == "carrier"
    assert exp.quote_id == "lane-1"
    assert exp.input_snapshot["route"] == "上海→杭州"

    ev = wb.events.filter(event_type="dispatched").first()
    assert ev is not None
    assert ev.payload["dispatch_type"] == "third_party"
    assert ev.payload["agreed_payable"] == 1850.0


@pytest.mark.django_db
def test_platform_dispatch_status_and_no_carrier_needed():
    from apps.ops.order_dispatch import dispatch_order

    wb = dispatch_order(_pooled(), dispatch_type="platform", platform_name="满帮", agreed_payable_amount=1600)
    assert wb.dispatch_status == "pending_accept"
    exp = ExpenseRecord.objects.get(waybill=wb, direction="payable")
    assert exp.payee_type == "platform"
    assert exp.payee_ref == "满帮"


@pytest.mark.django_db
def test_generate_costs_stores_rule_snapshot():
    from apps.finance.services import generate_costs

    carrier = Carrier.objects.create(code="C9", name="乙车队")
    Vehicle.objects.create(plate_no="沪A0001", load_capacity_ton=20)
    PricingRule.objects.create(
        name="沪杭成本价", price_type=PricingRule.PRICE_TYPE_COST, charge_method=PricingRule.METHOD_FLAT,
        expense_item_code="freight", carrier=carrier, route_name="上海→杭州", base_price=Decimal("1800"),
    )
    wb = Waybill.objects.create(waybill_no="WBS-1", route_name="上海→杭州", origin="上海", destination="杭州",
                                carrier=carrier, cargo_weight_ton=Decimal("6"))
    generate_costs(wb)
    exp = ExpenseRecord.objects.get(waybill=wb, direction="payable")
    assert exp.price_source == "rule"
    assert exp.pricing_rule_name == "沪杭成本价"
    assert exp.charge_method == PricingRule.METHOD_FLAT
    assert "承运商:乙车队" in exp.matched_condition
    assert exp.input_snapshot["weight_ton"] == 6.0
    assert exp.rule_snapshot["base_price"] == 1800.0


@pytest.mark.django_db
def test_batch_plan_recommends_carrier_per_lane():
    from apps.ops.order_dispatch import plan_dispatch_orders

    Vehicle.objects.create(plate_no="沪B0001", load_capacity_ton=30, volume_capacity_cbm=60)
    Carrier.objects.create(code="CB", name="拼单车队")
    plan = plan_dispatch_orders([_pooled(weight=8), _pooled(weight=6)])
    for trip in plan["consolidated_trips"]:
        assert "carrier_recommendation" in trip
        assert "carrier_candidates" in trip


@pytest.mark.django_db
def test_physics_city_typo_removed():
    from apps.ops.intake import standardize_and_enrich_addresses

    data = {"pickup_address": "某某物理研究所仓库", "delivery_address": "杭州市萧山区某仓"}
    standardize_and_enrich_addresses(data)
    # 「物理」不再被误判为城市；目的地应正确提取为杭州
    assert data.get("origin") != "物理"
    assert data.get("destination") == "杭州"
