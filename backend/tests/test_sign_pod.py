"""司机/客户签收回传（e-POD）→ 运单签收 → 订单完成闭环。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.core.exceptions import AppError
from apps.masterdata.models import Carrier
from apps.ops.intake import create_order_from_intake, pool_order
from apps.ops.models import Order, Receipt, Waybill
from apps.ops.order_dispatch import dispatch_order
from apps.ops.services import sign_waybill

User = get_user_model()


@pytest.fixture
def admin_client():
    User.objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def _dispatched_waybill():
    carrier = Carrier.objects.create(code="C1", name="承运")
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", "cargo_weight_ton": 5})
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)
    return dispatch_order(order, dispatch_type=Waybill.DISPATCH_THIRD_PARTY, carrier=carrier), order


@pytest.mark.django_db
def test_sign_completes_order_and_creates_receipt():
    waybill, order = _dispatched_waybill()
    waybill.status = Waybill.STATUS_IN_TRANSIT
    waybill.save()

    receipt = sign_waybill(waybill, signatory="张三", signature="data:image/png;base64,xxx", sign_source="driver")
    waybill.refresh_from_db()
    order.refresh_from_db()

    assert waybill.status == Waybill.STATUS_SIGNED
    assert waybill.receipt_status == "received"
    assert order.status == Order.STATUS_COMPLETED  # 签收触发订单完成
    assert receipt.signatory == "张三"
    assert Receipt.objects.filter(waybill=waybill, status="confirmed").count() == 1


@pytest.mark.django_db
def test_sign_rejected_when_not_in_transit():
    waybill, _ = _dispatched_waybill()  # pending_dispatch
    with pytest.raises(AppError):
        sign_waybill(waybill, signatory="李四")


@pytest.mark.django_db
def test_sign_endpoint(admin_client):
    waybill, _ = _dispatched_waybill()
    waybill.status = Waybill.STATUS_ARRIVED
    waybill.save()
    resp = admin_client.post(
        f"/api/v1/waybills/{waybill.waybill_no}/sign",
        {"signatory": "王五", "sign_source": "customer"},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert resp.json()["data"]["status"] == Waybill.STATUS_SIGNED
