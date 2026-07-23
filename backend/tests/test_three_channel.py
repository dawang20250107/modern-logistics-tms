"""承运三通道：网货平台派单 + 运单列表应收/应付聚合 + 通道大类。"""

import pytest
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord
from apps.ops.models import Order, Waybill


def _pooled_order():
    from apps.ops.intake import create_order_from_intake, pool_order

    order = create_order_from_intake(fields={"origin": "上海", "destination": "南京", "cargo_weight_ton": 6})
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)
    return order


@pytest.mark.django_db
def test_platform_dispatch_records_platform_info():
    from apps.ops.order_dispatch import dispatch_order

    order = _pooled_order()
    wb = dispatch_order(order, dispatch_type="platform", platform_name="满帮", platform_order_no="YMM-2026-001")
    assert wb.dispatch_type == "platform"
    assert wb.platform_name == "满帮"
    assert wb.platform_order_no == "YMM-2026-001"
    # 无车/司机/承运商也能网货派单成功（合规由平台承担）
    assert wb.carrier_id is None and wb.driver_id is None


@pytest.mark.django_db
def test_channel_label_mapping():
    assert Waybill.CHANNEL_LABELS["own_vehicle"] == "自营"
    assert Waybill.CHANNEL_LABELS["fleet"] == "自营"
    assert Waybill.CHANNEL_LABELS["third_party"] == "外包"
    assert Waybill.CHANNEL_LABELS["platform"] == "网货"


@pytest.mark.django_db
def test_waybill_list_annotates_receivable_payable_and_channel():
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    tok = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")

    wb = Waybill.objects.create(waybill_no="WB-CH-1", route_name="r", origin="上海", destination="南京",
                                dispatch_type="platform", platform_name="满帮")
    ExpenseRecord.objects.create(waybill=wb, direction="receivable", expense_item_code="freight", amount=3000)
    ExpenseRecord.objects.create(waybill=wb, direction="payable", expense_item_code="freight", amount=2400)
    ExpenseRecord.objects.create(waybill=wb, direction="payable", expense_item_code="fuel", amount=100)

    resp = client.get("/api/v1/waybills?page_size=50")
    assert resp.status_code == 200, resp.content
    row = next(r for r in resp.json()["data"]["items"] if r["waybill_no"] == "WB-CH-1")
    assert row["channel"] == "网货"
    assert row["dispatch_type_label"] == "网货平台"
    assert row["platform_name"] == "满帮"
    assert row["receivable_amount"] == 3000.0
    assert row["payable_amount"] == 2500.0  # 2400 + 100


@pytest.mark.django_db
def test_order_funnel_counts_by_status_and_channel():
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="b", password="pw-strong-123")
    client = APIClient()
    tok = client.post("/api/v1/auth/token", {"username": "b", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")

    Order.objects.create(order_no="OF1", status="pooled", channel="cs")
    Order.objects.create(order_no="OF2", status="pooled", channel="wechat_group")
    Order.objects.create(order_no="OF3", status="confirmed", channel="cs")

    data = client.get("/api/v1/orders/funnel").json()["data"]
    assert data["by_status"]["pooled"] == 2
    assert data["by_status"]["confirmed"] == 1
    assert data["by_channel"]["cs"] == 2
    assert data["total"] == 3
    assert data["today_created"] == 3
