"""P1-OPS-A：转运单带计划到达、dispatch 走状态机、多运单订单完单判定。"""

from datetime import timedelta

import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.ops.intake import convert_order_to_waybill
from apps.ops.models import Order, Waybill
from apps.ops.services import sign_waybill


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="ops_a", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "ops_a", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


@pytest.mark.django_db
def test_convert_sets_planned_arrival_from_order():
    eta = timezone.now() + timedelta(hours=8)
    order = Order.objects.create(order_no="OA1", origin="上海", destination="成都", expected_delivery_at=eta)
    wb = convert_order_to_waybill(order)
    assert wb.planned_arrival == eta  # 激活 ETA 偏移与准班率


@pytest.mark.django_db
def test_dispatch_endpoint_rejects_illegal_transition(admin_client):
    wb = Waybill.objects.create(waybill_no="DA1", route_name="r", status=Waybill.STATUS_PENDING_DISPATCH)
    # 直接跳到 settled 属非法流转 → 409（此前会被直写绕过）
    r = admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/dispatch", {"status": Waybill.STATUS_SETTLED}, format="json"
    )
    assert r.status_code == 409
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_PENDING_DISPATCH  # 未被改动


@pytest.mark.django_db
def test_dispatch_endpoint_legal_transition_goes_through_machine(admin_client):
    wb = Waybill.objects.create(waybill_no="DA2", route_name="r", status=Waybill.STATUS_PENDING_DISPATCH)
    r = admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/dispatch",
        {"status": Waybill.STATUS_DISPATCHED, "dispatch_status": "accepted"}, format="json",
    )
    assert r.status_code == 200
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_DISPATCHED
    assert wb.dispatch_status == "accepted"
    # 状态机事件已记录（非绕过）
    assert wb.events.filter(event_type="status_changed:dispatched").exists()


@pytest.mark.django_db
def test_multi_waybill_order_completes_only_when_all_signed():
    order = Order.objects.create(order_no="OA2", origin="上海", destination="成都")
    wb1 = Waybill.objects.create(waybill_no="MW1", route_name="r", order=order, status=Waybill.STATUS_ARRIVED)
    wb2 = Waybill.objects.create(waybill_no="MW2", route_name="r", order=order, status=Waybill.STATUS_ARRIVED)

    # 签收第一段 → 订单不应完成（第二段还在途）
    sign_waybill(wb1, signatory="甲")
    order.refresh_from_db()
    assert order.status != Order.STATUS_COMPLETED

    # 签收第二段 → 全部签收，订单完成
    sign_waybill(wb2, signatory="乙")
    order.refresh_from_db()
    assert order.status == Order.STATUS_COMPLETED


@pytest.mark.django_db
def test_cancelled_sibling_does_not_block_completion():
    order = Order.objects.create(order_no="OA3", origin="上海", destination="成都")
    Waybill.objects.create(waybill_no="MC1", route_name="r", order=order, status=Waybill.STATUS_CANCELLED)
    wb2 = Waybill.objects.create(waybill_no="MC2", route_name="r", order=order, status=Waybill.STATUS_ARRIVED)
    # 唯一有效运单签收 → 订单完成（作废运单不计入）
    sign_waybill(wb2, signatory="乙")
    order.refresh_from_db()
    assert order.status == Order.STATUS_COMPLETED
