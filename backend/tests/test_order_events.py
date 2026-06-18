"""订单事件溯源 + 派单防重复占用。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.core.exceptions import AppError
from apps.masterdata.models import Vehicle
from apps.ops.intake import create_order_from_intake, pool_order
from apps.ops.models import Order, OrderEvent, Waybill
from apps.ops.order_dispatch import dispatch_order

User = get_user_model()


@pytest.fixture
def admin_client():
    User.objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def _pooled(**kw):
    o = create_order_from_intake(fields={"origin": "上海", "destination": "成都", **kw})
    o.status = Order.STATUS_CONFIRMED
    o.save()
    return pool_order(o)


@pytest.mark.django_db
def test_lifecycle_events_recorded():
    order = create_order_from_intake(fields={"origin": "A", "destination": "B"})
    assert OrderEvent.objects.filter(order=order, event_type="created").exists()
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)
    types = list(OrderEvent.objects.filter(order=order).values_list("event_type", flat=True))
    assert "created" in types and "pooled" in types


@pytest.mark.django_db
def test_dispatch_rejects_busy_vehicle():
    vehicle = Vehicle.objects.create(plate_no="沪BUSY1", load_capacity_ton=20)
    # 先用该车派一单（运单进入占用状态）
    o1 = _pooled(cargo_weight_ton=5)
    dispatch_order(o1, dispatch_type=Waybill.DISPATCH_OWN, vehicle=vehicle)
    # 再用同一车派另一单 → 占用拒绝
    o2 = _pooled(cargo_weight_ton=5)
    with pytest.raises(AppError):
        dispatch_order(o2, dispatch_type=Waybill.DISPATCH_OWN, vehicle=vehicle)


@pytest.mark.django_db
def test_timeline_endpoint(admin_client):
    order = create_order_from_intake(fields={"origin": "A", "destination": "B"})
    resp = admin_client.get(f"/api/v1/orders/{order.id}/timeline")
    assert resp.status_code == 200, resp.content
    assert any(e["event_type"] == "created" for e in resp.json()["data"])
