"""企业级订单：结构化单号、企业字段、进池/取消、批量、软删。"""

import re

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.ops.intake import batch_orders, cancel_order, create_order_from_intake, pool_order
from apps.ops.models import Order


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_order_number_is_structured_and_sequential():
    o1 = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    o2 = create_order_from_intake(fields={"origin": "北京", "destination": "广州"})
    day = timezone.now().strftime("%Y%m%d")
    assert re.match(rf"DD{day}\d{{6}}", o1.order_no)
    assert int(o2.order_no[-6:]) == int(o1.order_no[-6:]) + 1  # 日序号递增、唯一


@pytest.mark.django_db
def test_create_order_with_enterprise_fields():
    order = create_order_from_intake(fields={
        "origin": "上海", "destination": "成都", "source_type": "government",
        "business_type": "coldchain", "priority": "vip", "settlement_type": "monthly",
        "temperature_range": "-18~0", "cargo_value": 50000, "is_hazardous": True,
        "delivery_address": "成都市高新区xx路1号",
    })
    assert order.source_type == "government"
    assert order.business_type == "coldchain"
    assert order.priority == "vip"
    assert order.is_hazardous is True
    assert order.delivery_address.endswith("1号")


@pytest.mark.django_db
def test_parse_then_manual_override_merge():
    # text 解析出 origin=杭州/destination=武汉，fields 覆盖 destination=南京
    order = create_order_from_intake(text="杭州到武汉 6吨", fields={"destination": "南京", "priority": "urgent"})
    assert order.origin == "杭州"
    assert order.destination == "南京"  # 手改优先
    assert order.priority == "urgent"


@pytest.mark.django_db
def test_pool_and_cancel():
    order = create_order_from_intake(fields={"origin": "A", "destination": "B"})
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)
    assert order.status == Order.STATUS_POOLED
    assert order.pooled_at is not None

    order2 = create_order_from_intake(fields={"origin": "A", "destination": "B"})
    cancel_order(order2)
    assert order2.status == Order.STATUS_CANCELLED


@pytest.mark.django_db
def test_batch_confirm_and_pool():
    ids = [create_order_from_intake(fields={"origin": "A", "destination": "B"}).id for _ in range(3)]
    r1 = batch_orders("confirm", ids)
    assert r1["ok_count"] == 3
    r2 = batch_orders("pool", ids)
    assert r2["ok_count"] == 3
    assert Order.objects.filter(status=Order.STATUS_POOLED).count() == 3


@pytest.mark.django_db
def test_soft_delete_endpoint(admin_client):
    order = create_order_from_intake(fields={"origin": "A", "destination": "B"})
    resp = admin_client.delete(f"/api/v1/orders/{order.id}")
    assert resp.status_code in (200, 204), resp.content
    assert Order.objects.filter(id=order.id).exists() is False  # 默认管理器过滤
    assert Order.all_objects.filter(id=order.id).exists() is True  # 审计可见


@pytest.mark.django_db
def test_batch_endpoint(admin_client):
    ids = [str(create_order_from_intake(fields={"origin": "A", "destination": "B"}).id) for _ in range(2)]
    resp = admin_client.post("/api/v1/orders/batch", {"action": "confirm", "ids": ids}, format="json")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["ok_count"] == 2
