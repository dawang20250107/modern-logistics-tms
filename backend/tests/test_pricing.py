"""合同价 / 计价规则 CRUD 端点。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.masterdata.models import Customer


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_pricing_rule_crud(admin_client):
    cust = Customer.objects.create(code="CP1", name="比亚迪")
    resp = admin_client.post("/api/v1/finance/pricing-rules", {
        "name": "沪蓉整车", "price_type": "income", "expense_item_code": "FREIGHT",
        "customer": str(cust.id), "route_name": "上海→成都", "base_price": "1000",
        "price_per_ton": "100", "min_price": "500", "priority": 10, "is_active": True,
    }, format="json")
    assert resp.status_code == 201, resp.content
    rid = resp.json()["data"]["id"]
    assert resp.json()["data"]["customer_name"] == "比亚迪"

    # 列表 + 类型过滤
    lst = admin_client.get("/api/v1/finance/pricing-rules?price_type=income")
    assert lst.json()["data"]["total"] == 1

    # PATCH 停用
    patched = admin_client.patch(f"/api/v1/finance/pricing-rules/{rid}", {"is_active": False}, format="json")
    assert patched.status_code == 200
    assert patched.json()["data"]["is_active"] is False

    # 删除
    d = admin_client.delete(f"/api/v1/finance/pricing-rules/{rid}")
    assert d.status_code in (200, 204)
