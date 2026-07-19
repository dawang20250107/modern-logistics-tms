"""承运商优先调度评分：默认建议翻转为外包承运商、按履约表现排序、风险与主管确认。"""

from datetime import timedelta
from decimal import Decimal

import pytest
from django.utils import timezone

from apps.finance.models import ExpenseRecord, PricingRule
from apps.masterdata.models import Carrier
from apps.ops.models import ExceptionRecord, Waybill


def _carrier(code, name, **kw):
    return Carrier.objects.create(code=code, name=name, **kw)


def _cost_rule(carrier, price):
    return PricingRule.objects.create(
        name=f"cost-{carrier.code}", price_type=PricingRule.PRICE_TYPE_COST,
        charge_method=PricingRule.METHOD_FLAT, expense_item_code="freight",
        carrier=carrier, base_price=Decimal(str(price)),
    )


_SEQ = {"n": 0}


def _wb(carrier, origin, dest, *, on_time=True, exc=False, receipt="returned", payable=None):
    _SEQ["n"] += 1
    now = timezone.now()
    wb = Waybill.objects.create(
        waybill_no=f"WBH-{carrier.code}-{_SEQ['n']}", route_name=f"{origin}-{dest}",
        origin=origin, destination=dest, carrier=carrier, dispatch_type="third_party",
        status="settled", planned_arrival=now,
        arrived_at=now - timedelta(hours=1) if on_time else now + timedelta(hours=4),
        receipt_status=receipt,
    )
    if payable is not None:
        ExpenseRecord.objects.create(waybill=wb, direction="payable", expense_item_code="freight", amount=Decimal(str(payable)))
    if exc:
        ExceptionRecord.objects.create(waybill=wb, exception_type="transit_delay", level="high")
    return wb


def _order(origin="上海", dest="杭州", weight=18):
    from apps.ops.intake import create_order_from_intake

    return create_order_from_intake(fields={"origin": origin, "destination": dest, "cargo_weight_ton": weight})


@pytest.mark.django_db
def test_suggested_type_flips_to_carrier_first():
    """有可用承运商时，默认建议应为外包承运商（不再默认自营车）。"""
    from apps.ops.order_dispatch import recommend_dispatch_for_order

    c = _carrier("C-GOOD", "华东顺捷")
    _cost_rule(c, 1800)
    for _ in range(5):
        _wb(c, "上海", "杭州", on_time=True, payable=1850)

    rec = recommend_dispatch_for_order(_order())
    assert rec["suggested_dispatch_type"] == "third_party"
    assert rec["recommendation"] is not None
    assert rec["recommendation"]["carrier"] == "华东顺捷"


@pytest.mark.django_db
def test_high_service_outranks_cheap_but_risky():
    """综合履约优于单纯价低：低价高异常承运商不应排在高服务承运商之前。"""
    from apps.ops.carrier_scoring import score_carriers

    good = _carrier("C-HS", "高服务车队")
    _cost_rule(good, 1900)
    for _ in range(6):
        _wb(good, "上海", "杭州", on_time=True, exc=False, receipt="returned", payable=1900)

    cheap = _carrier("C-CHEAP", "低价车队")
    _cost_rule(cheap, 1600)
    for i in range(6):
        _wb(cheap, "上海", "杭州", on_time=(i % 3 != 0), exc=(i % 2 == 0), receipt="pending", payable=1600)

    rows = score_carriers(_order())
    by_name = {r["carrier"]: r for r in rows}
    assert rows[0]["carrier"] == "高服务车队"
    assert by_name["高服务车队"]["score"] > by_name["低价车队"]["score"]
    assert by_name["低价车队"]["label"] == "低价有风险"
    assert by_name["低价车队"]["risk_level"] in ("medium", "high")


@pytest.mark.django_db
def test_blacklisted_and_expired_carriers_excluded():
    """黑名单 / 资质过期承运商不进入推荐（承运商风控硬阻断）。"""
    from apps.ops.carrier_scoring import score_carriers

    ok = _carrier("C-OK", "正常车队")
    _cost_rule(ok, 1800)
    _wb(ok, "上海", "杭州", payable=1800)

    black = _carrier("C-BL", "黑名单车队", blacklisted=True, blacklist_reason="多次货损")
    _cost_rule(black, 1000)
    expired = _carrier("C-EXP", "资质过期车队", qualification_expiry=timezone.localdate() - timedelta(days=5))
    _cost_rule(expired, 1000)

    names = {r["carrier"] for r in score_carriers(_order())}
    assert "正常车队" in names
    assert "黑名单车队" not in names
    assert "资质过期车队" not in names


@pytest.mark.django_db
def test_new_carrier_without_history_needs_approval():
    """无历史成交的承运商，推荐结论应要求主管确认。"""
    from apps.ops.order_dispatch import recommend_dispatch_for_order

    c = _carrier("C-NEW", "新合作车队")
    _cost_rule(c, 1800)  # 有报价但零历史

    rec = recommend_dispatch_for_order(_order())["recommendation"]
    assert rec is not None
    assert rec["needs_approval"] is True


@pytest.mark.django_db
def test_lane_price_library_drives_quote_and_frequent_routes():
    """线路价库标准价作为调度报价来源；常跑线路由历史聚合。"""
    from apps.masterdata.models import CarrierLanePrice
    from apps.ops.carrier_scoring import frequent_routes, score_carriers

    c = _carrier("C-LANE", "价库车队")
    CarrierLanePrice.objects.create(
        carrier=c, origin_city="上海", dest_city="杭州", vehicle_type="高栏",
        standard_price=1888, last_deal_price=1850, is_recommended=True,
    )
    for _ in range(4):
        _wb(c, "上海", "杭州", on_time=True, payable=1850)

    rows = score_carriers(_order())
    row = next(r for r in rows if r["carrier"] == "价库车队")
    assert row["quote"] == 1888.0  # 取自线路价库标准价
    assert row["from_lane_price"] is True
    assert row["lane_preferred"] is True

    routes = frequent_routes(c)
    assert routes and routes[0]["origin"] == "上海" and routes[0]["destination"] == "杭州"
    assert routes[0]["deals"] == 4


@pytest.mark.django_db
def test_carrier_endpoints_expose_new_profile_and_performance():
    """承运商序列化暴露类型/城市/到期预警；performance 端点返回经营表现。"""
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    from apps.masterdata.models import Carrier

    admin = get_user_model().objects.create_superuser(username="cadmin", password="pw-strong-123")
    client = APIClient()
    tok = client.post("/api/v1/auth/token", {"username": "cadmin", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")

    c = Carrier.objects.create(
        code="C-PROF", name="档案车队", carrier_type=Carrier.TYPE_COMPANY_FLEET, city="上海",
        insurance_expiry=timezone.localdate() - timedelta(days=2),  # 已过期 → 预警
    )
    for _ in range(3):
        _wb(c, "上海", "杭州", on_time=True)

    detail = client.get(f"/api/v1/carriers/{c.id}").json()["data"]
    assert detail["carrier_type_label"] == "公司车队"
    assert detail["city"] == "上海"
    assert any(a["field"] == "insurance_expiry" and a["expired"] for a in detail["expiry_alerts"])
    assert detail["performance"]["deals"] == 3

    perf = client.get(f"/api/v1/carriers/{c.id}/performance").json()["data"]
    assert perf["deals"] == 3
    assert perf["frequent_routes"][0]["destination"] == "杭州"
