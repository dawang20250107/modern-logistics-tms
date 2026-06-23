"""外部接入预留：飞书卡片构造 + 微信/飞书预留 + 接入状态。"""

import pytest

from apps.integrations import feishu, wechat
from apps.ops.intake import create_order_from_intake


@pytest.mark.django_db
def test_feishu_new_demand_card_structure():
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "cargo_weight_ton": 8})
    card = feishu.new_demand_card(order)
    assert card["header"]["title"]["content"].startswith("🚚")
    assert card["header"]["template"] == "blue"
    # 含「填写调度结果」按钮
    actions = card["elements"][-1]["actions"]
    assert actions[0]["value"]["action"] == "dispatch_fill"
    assert actions[0]["value"]["order_no"] == order.order_no


def test_feishu_all_four_cards_build():
    assert feishu.dispatch_result_card(type("O", (), {"order_no": "DD1"})(), vehicle="沪A1")["header"]["template"] == "green"
    exc = type("E", (), {"exception_type": "transit_delay", "level": "high",
                         "get_level_display": lambda self: "高", "waybill_id": None,
                         "description": "拥堵", "id": "x"})()
    assert feishu.exception_alert_card(exc)["header"]["template"] == "red"
    assert feishu.transfer_human_card(reason="超纲")["header"]["template"] == "orange"


def test_feishu_and_wechat_push_reserved():
    assert feishu.push_card(feishu.transfer_human_card())["status"] == "reserved"
    assert wechat.receive_group_message({})["status"] == "reserved"
    assert wechat.configured() is False  # 测试环境无凭证


@pytest.mark.django_db
def test_integration_status_endpoint(admin_client):
    resp = admin_client.get("/api/v1/integrations/status")
    assert resp.status_code == 200, resp.content
    states = {i["key"]: i["state"] for i in resp.json()["data"]["integrations"]}
    assert states["ymm"] == "fallback"        # 已实现但无凭证 → 离线
    assert states["feishu"] == "reserved"
    assert states["wechat"] == "reserved"


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="intg_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "intg_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c
