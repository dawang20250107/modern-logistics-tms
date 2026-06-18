"""批量建单导入。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.ops.intake import import_orders
from apps.ops.models import Order


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_import_orders_service():
    rows = [
        {"origin": "上海", "destination": "成都", "cargo_weight_ton": 10},
        {"origin": "北京", "destination": "广州", "cargo_weight_ton": 8},
        "bad-row",
    ]
    result = import_orders(rows, channel=Order.CHANNEL_CS)
    assert result["ok_count"] == 2
    assert result["failed_count"] == 1
    assert Order.objects.count() == 2


@pytest.mark.django_db
def test_import_endpoint(admin_client):
    resp = admin_client.post(
        "/api/v1/orders/import",
        {"channel": "miniprogram", "rows": [
            {"origin": "杭州", "destination": "武汉", "cargo_weight_ton": 6},
            {"origin": "南京", "destination": "苏州", "cargo_weight_ton": 3},
        ]},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert resp.json()["data"]["ok_count"] == 2
    assert Order.objects.filter(channel="miniprogram").count() == 2
