"""订单池 + 调度认领（并发安全）+ AI 派单建议 + 派单。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.core.exceptions import AppError
from apps.masterdata.models import Carrier, Vehicle
from apps.ops.intake import create_order_from_intake, pool_order
from apps.ops.models import Order, Waybill
from apps.ops.order_dispatch import (
    claim_order,
    dispatch_order,
    external_signals,
    recommend_dispatch_for_order,
)

User = get_user_model()


@pytest.fixture
def admin_client():
    User.objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def _pooled_order(**kw):
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", **kw})
    order.status = Order.STATUS_CONFIRMED
    order.save()
    return pool_order(order)


@pytest.mark.django_db
def test_claim_is_exclusive():
    d1 = User.objects.create_user(username="disp1", password="x")
    d2 = User.objects.create_user(username="disp2", password="x")
    order = _pooled_order()

    claimed = claim_order(order.id, d1)
    assert claimed.status == Order.STATUS_DISPATCHING
    assert claimed.claimed_by_id == d1.id

    # 第二名调度再抢 → 失败（行锁 + 状态校验保证一单一抢）
    with pytest.raises(AppError):
        claim_order(order.id, d2)


@pytest.mark.django_db
def test_external_signals_from_order_attrs():
    order = create_order_from_intake(fields={
        "origin": "A", "destination": "B", "is_hazardous": True,
        "business_type": "coldchain", "temperature_range": "-18~0", "priority": "vip",
    })
    types = {s["type"] for s in external_signals(order)}
    assert {"hazardous", "coldchain", "priority"} <= types


@pytest.mark.django_db
def test_recommend_dispatch_for_order():
    Vehicle.objects.create(plate_no="沪AP001", load_capacity_ton=20)
    Carrier.objects.create(code="CC", name="承运甲")
    order = _pooled_order(cargo_weight_ton=10)
    rec = recommend_dispatch_for_order(order)
    assert "vehicle_candidates" in rec
    assert rec["best_vehicle"]["plate_no"] == "沪AP001"
    assert rec["suggested_dispatch_type"] == "own_vehicle"


@pytest.mark.django_db
def test_dispatch_order_creates_waybill_with_type():
    carrier = Carrier.objects.create(code="C9", name="三方承运")
    order = _pooled_order(cargo_weight_ton=8)
    waybill = dispatch_order(order, dispatch_type=Waybill.DISPATCH_THIRD_PARTY, carrier=carrier)
    order.refresh_from_db()
    assert order.status == Order.STATUS_CONVERTED
    assert waybill.dispatch_type == Waybill.DISPATCH_THIRD_PARTY
    assert waybill.carrier_id == carrier.id
    assert waybill.status == Waybill.STATUS_PENDING_DISPATCH


@pytest.mark.django_db
def test_dispatch_rejects_overloaded_vehicle():
    order = _pooled_order(cargo_weight_ton=20)
    small = Vehicle.objects.create(plate_no="小面包", load_capacity_ton=3)
    with pytest.raises(AppError) as exc:
        dispatch_order(order, dispatch_type=Waybill.DISPATCH_OWN, vehicle=small)
    assert exc.value.code == "VEHICLE_OVERLOADED"
    order.refresh_from_db()
    assert order.status != Order.STATUS_CONVERTED  # 未误派


@pytest.mark.django_db
def test_pool_and_claim_endpoints(admin_client):
    _pooled_order()
    resp = admin_client.get("/api/v1/orders/pool")
    assert resp.status_code == 200, resp.content
    items = resp.json()["data"]["items"]
    assert len(items) == 1
    oid = items[0]["id"]

    resp = admin_client.post(f"/api/v1/orders/{oid}/claim")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["status"] == Order.STATUS_DISPATCHING

    # 认领后仍在池中可见（DISPATCHING），且 mine 过滤命中
    pool = admin_client.get("/api/v1/orders/pool")
    assert len(pool.json()["data"]["items"]) == 1
    mine = admin_client.get("/api/v1/orders/pool?mine=1")
    assert len(mine.json()["data"]["items"]) == 1

    # 退回订单池
    rel = admin_client.post(f"/api/v1/orders/{oid}/release")
    assert rel.status_code == 200, rel.content
    assert rel.json()["data"]["status"] == Order.STATUS_POOLED


@pytest.mark.django_db
def test_dispatch_plan_assigns_vehicles(admin_client):
    Vehicle.objects.create(plate_no="排线A", load_capacity_ton=20)
    Vehicle.objects.create(plate_no="排线B", load_capacity_ton=20)
    o1 = _pooled_order(cargo_weight_ton=15)
    o2 = _pooled_order(cargo_weight_ton=8)
    o3 = _pooled_order(cargo_weight_ton=5)
    resp = admin_client.post("/api/v1/orders/dispatch-plan", {"ids": [str(o1.id), str(o2.id), str(o3.id)]}, format="json")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["assigned_count"] == 2  # 仅 2 辆车
    assert data["unassigned_count"] == 1
    assert all("vehicle" in a for a in data["assignments"])


@pytest.mark.django_db
def test_signing_completes_order():
    from apps.ops.services import transition_waybill

    carrier = Carrier.objects.create(code="C7", name="承运丙")
    order = _pooled_order(cargo_weight_ton=5)
    waybill = dispatch_order(order, dispatch_type=Waybill.DISPATCH_THIRD_PARTY, carrier=carrier)
    # 推进到已到达，再签收回传 → 订单回写完成
    waybill.status = Waybill.STATUS_ARRIVED
    waybill.save()
    transition_waybill(waybill, Waybill.STATUS_SIGNED)
    order.refresh_from_db()
    assert order.status == Order.STATUS_COMPLETED


@pytest.mark.django_db
def test_dispatch_endpoint(admin_client):
    carrier = Carrier.objects.create(code="C8", name="承运乙")
    order = _pooled_order(cargo_weight_ton=5)
    resp = admin_client.post(
        f"/api/v1/orders/{order.id}/dispatch",
        {"dispatch_type": "third_party", "carrier": str(carrier.id)},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert resp.json()["data"]["status"] == Waybill.STATUS_PENDING_DISPATCH
