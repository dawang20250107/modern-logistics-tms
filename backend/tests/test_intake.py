"""多渠道建单（AI/规则解析 + 转运单）测试。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.ops.intake import (
    convert_order_to_waybill,
    create_order_from_intake,
    parse_order_text_rule,
)
from apps.ops.models import Order, Waybill


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def test_parse_order_text_rule_extracts_fields():
    text = "上海到成都，10吨货，5件，电话13800001234"
    fields = parse_order_text_rule(text)
    assert fields["origin"] == "上海"
    assert fields["destination"] == "成都"
    assert fields["cargo_weight_ton"] == 10.0
    assert fields["cargo_quantity"] == 5
    assert fields["contact_phone"] == "13800001234"


@pytest.mark.django_db
def test_create_order_from_intake_text():
    order = create_order_from_intake(
        text="北京发广州 8吨 联系18900002222", channel=Order.CHANNEL_WECHAT_GROUP, source="华东群"
    )
    assert order.status == Order.STATUS_PENDING_CONFIRM
    assert order.channel == Order.CHANNEL_WECHAT_GROUP
    assert order.origin == "北京"
    assert order.destination == "广州"
    assert order.parse_meta["source"] == "rule"
    assert order.raw_text


@pytest.mark.django_db
def test_convert_order_to_waybill():
    order = create_order_from_intake(text="上海到成都 10吨", channel=Order.CHANNEL_CS)
    waybill = convert_order_to_waybill(order)
    order.refresh_from_db()
    assert order.status == Order.STATUS_CONVERTED
    assert waybill.order_id == order.id
    assert waybill.status == Waybill.STATUS_PENDING_DISPATCH
    assert float(waybill.cargo_weight_ton) == 10.0
    assert waybill.route_name == "上海→成都"


@pytest.mark.django_db
def test_intake_and_convert_endpoints(admin_client):
    resp = admin_client.post(
        "/api/v1/orders/intake",
        {"text": "杭州到武汉 6吨 3件 电话13700001111", "channel": "miniprogram"},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    data = resp.json()["data"]
    assert data["channel"] == "miniprogram"
    assert data["origin"] == "杭州"
    order_id = data["id"]

    admin_client.post(f"/api/v1/orders/{order_id}/confirm")
    resp = admin_client.post(f"/api/v1/orders/{order_id}/convert")
    assert resp.status_code == 201, resp.content
    assert resp.json()["data"]["status"] == Waybill.STATUS_PENDING_DISPATCH


@pytest.mark.django_db
def test_parse_preview_endpoint(admin_client):
    resp = admin_client.post(
        "/api/v1/orders/parse-preview", {"text": "成都到重庆 12吨"}, format="json"
    )
    assert resp.status_code == 200, resp.content
    body = resp.json()["data"]
    assert body["fields"]["origin"] == "成都"
    assert body["meta"]["source"] == "rule"


@pytest.mark.django_db
def test_parse_preview_flags_missing_fields(admin_client):
    # 只给始发+货量，缺目的地与电话 → AI 提示补充
    resp = admin_client.post("/api/v1/orders/parse-preview", {"text": "上海 10吨"}, format="json")
    labels = {m["label"] for m in resp.json()["data"]["missing"]}
    assert "联系电话" in labels
