"""计费方式：整车一口价 / 阶梯重 / 按方 / 按件 / 按公里 / 吨公里 + 最低计费量。"""

from decimal import Decimal

import pytest

from apps.finance.models import PricingRule


def _rule(**kw):
    kw.setdefault("name", "r")
    kw.setdefault("price_type", PricingRule.PRICE_TYPE_INCOME)
    kw.setdefault("expense_item_code", "TRANSPORT_INCOME")
    return PricingRule(**kw)


@pytest.mark.django_db
def test_flat_ignores_quantity():
    r = _rule(charge_method=PricingRule.METHOD_FLAT, base_price=Decimal("3000"))
    q = r.quote(weight_ton=10, volume_cbm=40, quantity=5, distance_km=800)
    assert q["amount"] == Decimal("3000.00")
    assert q["charge_method"] == "flat"


@pytest.mark.django_db
def test_per_volume_with_min_charge_qty():
    r = _rule(charge_method=PricingRule.METHOD_PER_VOLUME, unit_price=Decimal("120"), min_charge_qty=Decimal("5"))
    # 3 方不足最低 5 方 → 按 5 方计
    q = r.quote(weight_ton=1, volume_cbm=3)
    assert q["billable_volume"] == 5.0
    assert q["amount"] == Decimal("600.00")
    # 8 方 → 按 8 方
    assert r.quote(weight_ton=1, volume_cbm=8)["amount"] == Decimal("960.00")


@pytest.mark.django_db
def test_per_piece():
    r = _rule(charge_method=PricingRule.METHOD_PER_PIECE, unit_price=Decimal("15"), base_price=Decimal("50"))
    q = r.quote(weight_ton=1, quantity=100)
    assert q["billable_pieces"] == 100.0
    assert q["amount"] == Decimal("1550.00")  # 50 + 15*100


@pytest.mark.django_db
def test_per_km():
    r = _rule(charge_method=PricingRule.METHOD_PER_KM, unit_price=Decimal("8"))
    q = r.quote(weight_ton=10, distance_km=500)
    assert q["distance_km"] == 500.0
    assert q["amount"] == Decimal("4000.00")


@pytest.mark.django_db
def test_per_ton_km():
    r = _rule(charge_method=PricingRule.METHOD_PER_TON_KM, unit_price=Decimal("0.5"))
    # 计费吨 = max(10, 抛重20*0.3333≈6.67) = 10；10吨 × 600km × 0.5
    q = r.quote(weight_ton=10, volume_cbm=20, distance_km=600)
    assert q["ton_km"] == 6000.0
    assert q["amount"] == Decimal("3000.00")


@pytest.mark.django_db
def test_tiered_weight_still_works_and_is_default():
    r = _rule(tier_prices=[{"min_ton": 0, "max_ton": 5, "price": 200}, {"min_ton": 5, "max_ton": 999, "price": 180}])
    assert r.charge_method == PricingRule.METHOD_TIERED_WEIGHT
    # 8 吨 → 命中第二档 180 → 8*180
    assert r.quote(weight_ton=8)["amount"] == Decimal("1440.00")


@pytest.mark.django_db
def test_min_price_floor_and_fuel_surcharge():
    r = _rule(charge_method=PricingRule.METHOD_PER_KM, unit_price=Decimal("1"),
              min_price=Decimal("500"), fuel_surcharge_pct=Decimal("0.1"))
    # 100km*1 = 100 < 500 下限 → 500，再 +10% 燃油 = 550
    q = r.quote(weight_ton=1, distance_km=100)
    assert q["freight_amount"] == Decimal("500.00")
    assert q["fuel_surcharge"] == Decimal("50.00")
    assert q["amount"] == Decimal("550.00")


@pytest.mark.django_db
def test_estimate_order_quote_reports_method():
    from apps.finance.services import estimate_order_quote
    from apps.masterdata.models import Customer

    cust = Customer.objects.create(code="PMQ", name="计费客户")
    PricingRule.objects.create(
        name="整车价", price_type=PricingRule.PRICE_TYPE_INCOME, expense_item_code="TRANSPORT_INCOME",
        charge_method=PricingRule.METHOD_FLAT, base_price=Decimal("2600"), customer=cust, priority=10,
    )
    result = estimate_order_quote(customer_id=str(cust.id), weight_ton=12)
    assert result["matched"] is True
    assert result["amount"] == 2600.0
    assert result["charge_method"] == "flat"
    assert result["charge_method_label"] == "整车一口价"
