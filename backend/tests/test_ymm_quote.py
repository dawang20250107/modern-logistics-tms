"""运满满调车运费比价接口：离线回退 + 签名 + 派单建议接入。"""

import pytest

from apps.integrations.ymm import _sign, freight_quote
from apps.ops.intake import create_order_from_intake
from apps.ops.models import Order


@pytest.mark.django_db
def test_freight_quote_offline_fallback_without_credentials():
    # 测试环境无凭证 → 离线参考价（不发起网络请求）
    q = freight_quote("上海", "成都", weight_ton=10, volume_cbm=20)
    assert q["source"] == "offline"
    assert q["provider"].startswith("运满满")
    assert q["low"] < q["avg"] < q["high"]
    assert q["route"] == "上海→成都"
    assert q["currency"] == "CNY"


def test_sign_deterministic_and_empty_without_secret():
    params = {"appKey": "k", "method": "m", "ts": "1"}
    assert _sign(params, "") == ""
    s1 = _sign(params, "secret")
    s2 = _sign(params, "secret")
    assert s1 == s2 and len(s1) == 64


@pytest.mark.django_db
def test_ymm_quote_endpoint(admin_client):
    order = create_order_from_intake(
        fields={"origin": "上海", "destination": "成都", "cargo_weight_ton": 8},
        status=Order.STATUS_POOLED,
    )
    resp = admin_client.get(f"/api/v1/orders/{order.id}/ymm-quote")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["source"] in ("offline", "ymm")


@pytest.mark.django_db
def test_dispatch_suggestion_includes_ymm(admin_client):
    order = create_order_from_intake(
        fields={"origin": "上海", "destination": "成都", "cargo_weight_ton": 8},
        status=Order.STATUS_POOLED,
    )
    resp = admin_client.get(f"/api/v1/orders/{order.id}/dispatch-suggestion")
    assert resp.status_code == 200
    assert "ymm_quote" in resp.json()["data"]
    assert resp.json()["data"]["ymm_quote"]["avg"] > 0


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="ymm_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "ymm_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c
