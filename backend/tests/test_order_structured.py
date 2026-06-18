"""企业级录单：多货物明细 / 多站点 / 自动报价 / 模板。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.finance.models import PricingRule
from apps.finance.services import estimate_order_quote
from apps.masterdata.models import Customer
from apps.ops.intake import create_order_from_intake, recompute_cargo_totals
from apps.ops.models import OrderCargoItem, OrderStop


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_recompute_cargo_totals_sums_items():
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    OrderCargoItem.objects.create(order=order, seq=1, name="钢材", quantity=10, weight_ton=5, volume_cbm=3)
    OrderCargoItem.objects.create(order=order, seq=2, name="木材", quantity=4, weight_ton=2, volume_cbm=6)
    recompute_cargo_totals(order)
    order.refresh_from_db()
    assert order.cargo_quantity == 14
    assert float(order.cargo_weight_ton) == 7.0
    assert float(order.cargo_volume_cbm) == 9.0


@pytest.mark.django_db
def test_estimate_order_quote_matches_rule():
    cust = Customer.objects.create(code="CQ1", name="比亚迪")
    PricingRule.objects.create(
        name="沪蓉整车", price_type=PricingRule.PRICE_TYPE_INCOME, expense_item_code="FREIGHT",
        customer=cust, base_price=1000, price_per_ton=100, priority=10,
    )
    q = estimate_order_quote(customer_id=cust.id, route_name="上海→成都", weight_ton=8)
    assert q["matched"] is True
    assert q["amount"] == 1800.0  # 1000 + 100*8
    assert q["rule_name"] == "沪蓉整车"


@pytest.mark.django_db
def test_estimate_order_quote_no_match():
    q = estimate_order_quote(customer_id=None, route_name="x", weight_ton=5)
    assert q["matched"] is False
    assert q["amount"] == 0.0


@pytest.mark.django_db
def test_order_detail_exposes_cargo_items_and_stops(admin_client):
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    OrderCargoItem.objects.create(order=order, seq=1, name="钢材", quantity=10, weight_ton=5)
    OrderStop.objects.create(order=order, seq=1, stop_type=OrderStop.STOP_PICKUP, city="上海", address="A仓")
    OrderStop.objects.create(order=order, seq=2, stop_type=OrderStop.STOP_DELIVERY, city="成都", address="B仓")
    resp = admin_client.get(f"/api/v1/orders/{order.id}")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert len(data["cargo_items"]) == 1
    assert data["cargo_items"][0]["name"] == "钢材"
    assert len(data["stops"]) == 2
    assert data["stops"][1]["stop_type"] == "delivery"
