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
def test_intake_with_cargo_items_and_draft(admin_client):
    resp = admin_client.post("/api/v1/orders/intake", {
        "channel": "cs",
        "status": "draft",
        "fields": {"origin": "上海", "destination": "成都", "business_type": "ftl"},
        "cargo_items": [
            {"name": "钢材", "quantity": 10, "weight_ton": 5, "volume_cbm": 3},
            {"name": "木材", "quantity": 4, "weight_ton": 2},
        ],
        "stops": [
            {"stop_type": "pickup", "city": "上海", "address": "A仓", "contact_phone": "13800001234"},
            {"stop_type": "delivery", "city": "成都", "address": "B仓"},
        ],
    }, format="json")
    assert resp.status_code == 201, resp.content
    data = resp.json()["data"]
    assert data["status"] == "draft"
    assert len(data["cargo_items"]) == 2
    assert len(data["stops"]) == 2
    assert float(data["cargo_weight_ton"]) == 7.0  # 汇总
    assert data["cargo_quantity"] == 14


@pytest.mark.django_db
def test_quote_endpoint(admin_client):
    cust = Customer.objects.create(code="CQ2", name="宁德时代")
    PricingRule.objects.create(
        name="沪蓉", price_type=PricingRule.PRICE_TYPE_INCOME, expense_item_code="FREIGHT",
        customer=cust, base_price=500, price_per_ton=200, priority=5,
    )
    resp = admin_client.post("/api/v1/orders/quote", {
        "customer": str(cust.id), "origin": "上海", "destination": "成都", "cargo_weight_ton": 10,
    }, format="json")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["amount"] == 2500.0  # 500 + 200*10


@pytest.mark.django_db
def test_edit_and_clone_order(admin_client):
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    OrderCargoItem.objects.create(order=order, seq=1, name="旧货", quantity=1, weight_ton=1)
    # 编辑：替换货物明细
    resp = admin_client.post(f"/api/v1/orders/{order.id}/edit", {
        "fields": {"priority": "urgent"},
        "cargo_items": [{"name": "新货", "quantity": 3, "weight_ton": 6}],
    }, format="json")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["priority"] == "urgent"
    assert len(data["cargo_items"]) == 1
    assert data["cargo_items"][0]["name"] == "新货"
    assert float(data["cargo_weight_ton"]) == 6.0
    # 复制建单 → 新草稿
    resp2 = admin_client.post(f"/api/v1/orders/{order.id}/clone")
    assert resp2.status_code == 201, resp2.content
    clone = resp2.json()["data"]
    assert clone["status"] == "draft"
    assert clone["order_no"] != order.order_no
    assert len(clone["cargo_items"]) == 1


@pytest.mark.django_db
def test_order_template_crud(admin_client):
    resp = admin_client.post("/api/v1/order-templates", {
        "name": "沪蓉整车模板",
        "payload": {"fields": {"origin": "上海", "destination": "成都", "business_type": "ftl"}},
    }, format="json")
    assert resp.status_code == 201, resp.content
    resp2 = admin_client.get("/api/v1/order-templates")
    assert resp2.status_code == 200
    assert resp2.json()["data"]["total"] >= 1


@pytest.mark.django_db
def test_export_csv(admin_client):
    create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    resp = admin_client.get("/api/v1/orders/export")
    assert resp.status_code == 200
    assert "text/csv" in resp["Content-Type"]
    assert "订单号" in resp.content.decode("utf-8")


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
