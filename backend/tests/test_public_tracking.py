"""客户自助订单跟踪（免登录）。"""

import pytest
from rest_framework.test import APIClient

from apps.ops.intake import create_order_from_intake


@pytest.fixture
def client():
    return APIClient()  # 不鉴权，验证公开端点


@pytest.mark.django_db
def test_track_requires_matching_phone(client):
    order = create_order_from_intake(fields={
        "origin": "上海", "destination": "成都", "contact_phone": "13800001234",
    })
    # 正确手机号 → 命中
    resp = client.get(f"/api/v1/track?order_no={order.order_no}&phone=13800001234")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["order_no"] == order.order_no
    assert data["origin"] == "上海"
    assert any(m["event"] == "created" for m in data["milestones"])

    # 后四位也可
    resp = client.get(f"/api/v1/track?order_no={order.order_no}&phone=1234")
    assert resp.status_code == 200


@pytest.mark.django_db
def test_track_wrong_phone_404(client):
    order = create_order_from_intake(fields={"origin": "A", "destination": "B", "contact_phone": "13800001234"})
    resp = client.get(f"/api/v1/track?order_no={order.order_no}&phone=9999")
    assert resp.status_code == 404


@pytest.mark.django_db
def test_track_missing_params(client):
    resp = client.get("/api/v1/track?order_no=DD1")
    assert resp.status_code == 400
