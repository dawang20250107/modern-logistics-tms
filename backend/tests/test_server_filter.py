"""服务端筛选：FilterBuilder 模型 → ORM 查询，以及 /orders 列表接口筛选/分页/排序。"""

import json
from decimal import Decimal

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.core.filtering import FilterField, apply_filter_model
from apps.masterdata.models import Customer
from apps.ops.models import Order


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


FIELDS = {
    "customer": FilterField("text", "customer__name"),
    "route": FilterField("text", paths=["origin", "destination"]),
    "status": FilterField("enum", "status"),
    "amount": FilterField("number", "quoted_amount"),
}


@pytest.mark.django_db
def test_apply_filter_model_ops():
    c1 = Customer.objects.create(code="F1", name="美的集团")
    c2 = Customer.objects.create(code="F2", name="海尔智家")
    Order.objects.create(order_no="D1", customer=c1, status="completed", origin="上海", destination="西安", quoted_amount=Decimal("20000"))
    Order.objects.create(order_no="D2", customer=c1, status="pooled", origin="杭州", destination="重庆", quoted_amount=Decimal("8000"))
    Order.objects.create(order_no="D3", customer=c2, status="completed", origin="苏州", destination="西安", quoted_amount=Decimal("15000"))

    base = Order.objects.all()
    # text contains
    m = {"combinator": "and", "conditions": [{"field": "customer", "op": "contains", "value": "美的"}]}
    assert apply_filter_model(base, json.dumps(m), FIELDS).count() == 2
    # multi-path text (route over origin+destination)
    m = {"combinator": "and", "conditions": [{"field": "route", "op": "contains", "value": "西安"}]}
    assert apply_filter_model(base, m, FIELDS).count() == 2
    # enum in
    m = {"combinator": "and", "conditions": [{"field": "status", "op": "in", "value": ["completed"]}]}
    assert apply_filter_model(base, m, FIELDS).count() == 2
    # number between
    m = {"combinator": "and", "conditions": [{"field": "amount", "op": "between", "value": ["10000", "18000"]}]}
    assert apply_filter_model(base, m, FIELDS).count() == 1
    # AND of two conditions
    m = {"combinator": "and", "conditions": [
        {"field": "customer", "op": "contains", "value": "美的"},
        {"field": "status", "op": "in", "value": ["completed"]},
    ]}
    assert apply_filter_model(base, m, FIELDS).count() == 1
    # OR of two conditions
    m = {"combinator": "or", "conditions": [
        {"field": "customer", "op": "contains", "value": "美的"},
        {"field": "customer", "op": "contains", "value": "海尔"},
    ]}
    assert apply_filter_model(base, m, FIELDS).count() == 3
    # unknown field ignored; bad JSON returns base
    assert apply_filter_model(base, "{bad json", FIELDS).count() == 3


@pytest.mark.django_db
def test_orders_endpoint_server_filter_sort_page(admin_client):
    c1 = Customer.objects.create(code="F3", name="美的集团")
    for i, amt in enumerate([25000, 23000, 21000, 19000, 17000, 15000]):
        Order.objects.create(order_no=f"DA{i}", customer=c1, status="pooled", quoted_amount=Decimal(str(amt)))
    Customer.objects.create(code="F4", name="别的客户")
    Order.objects.create(order_no="DB0", customer=Customer.objects.get(code="F4"), status="pooled", quoted_amount=Decimal("99999"))

    flt = json.dumps({"combinator": "and", "conditions": [{"field": "customer", "op": "contains", "value": "美的"}]})
    resp = admin_client.get("/api/v1/orders", {"filter": flt, "ordering": "-quoted_amount", "page_size": 4, "page": 1})
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["total"] == 6
    assert data["pages"] == 2
    assert len(data["items"]) == 4
    # sorted desc by amount, only 美的
    amts = [float(o["quoted_amount"]) for o in data["items"]]
    assert amts == sorted(amts, reverse=True)
    assert all("美的" in o["customer_name"] for o in data["items"])

    resp2 = admin_client.get("/api/v1/orders", {"filter": flt, "page_size": 4, "page": 2})
    assert len(resp2.json()["data"]["items"]) == 2
