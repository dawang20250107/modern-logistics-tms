"""阶段二：点位到达时间自动化（GPS 围栏盖戳 + 里程碑物化 + 点位拷贝）。"""

import pytest

from apps.ops.geofence import haversine_m, process_point
from apps.ops.intake import convert_order_to_waybill, create_order_from_intake
from apps.ops.models import Order, OrderStop, Waybill, WaybillStop
from apps.ops.services import transition_waybill


def _convertible_order(**kw):
    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都", **kw})
    order.status = Order.STATUS_CONFIRMED
    order.save(update_fields=["status"])
    return order


@pytest.mark.django_db
def test_haversine_roughly_correct():
    # 上海人广 → 约 1 公里外
    d = haversine_m(31.2304, 121.4737, 31.2394, 121.4737)
    assert 950 < d < 1050


@pytest.mark.django_db
def test_stops_copied_on_convert():
    order = _convertible_order()
    OrderStop.objects.create(order=order, seq=1, stop_type="pickup", city="上海", address="A 仓")
    OrderStop.objects.create(order=order, seq=2, stop_type="delivery", city="成都", address="B 仓")
    waybill = convert_order_to_waybill(order)
    stops = list(waybill.stops.order_by("seq"))
    assert [s.address for s in stops] == ["A 仓", "B 仓"]
    assert all(s.actual_arrival_at is None for s in stops)


@pytest.mark.django_db
def test_geofence_stamps_arrival_and_departure():
    wb = Waybill.objects.create(waybill_no="GEO1", route_name="r")
    stop = WaybillStop.objects.create(
        waybill=wb, seq=1, stop_type="delivery", address="收货点",
        lat="31.230400", lng="121.473700", radius_m=800,
    )
    # 进入围栏 → 盖到达
    changes = process_point(wb, 31.2305, 121.4738)
    assert changes == [{"seq": 1, "event": "arrived"}]
    stop.refresh_from_db()
    assert stop.actual_arrival_at is not None
    assert stop.arrival_source == WaybillStop.SRC_GPS
    assert stop.status == WaybillStop.STATUS_ARRIVED
    # 仍在围栏内 → 不重复
    assert process_point(wb, 31.2306, 121.4739) == []
    # 离开围栏（约 5km 外）→ 盖离开
    changes = process_point(wb, 31.2750, 121.4737)
    assert changes == [{"seq": 1, "event": "departed"}]
    stop.refresh_from_db()
    assert stop.actual_depart_at is not None
    assert stop.status == WaybillStop.STATUS_DEPARTED


@pytest.mark.django_db
def test_milestone_times_materialized_on_transition():
    wb = Waybill.objects.create(waybill_no="MILE1", route_name="r", status=Waybill.STATUS_DISPATCHED)
    transition_waybill(wb, Waybill.STATUS_LOADED)
    transition_waybill(wb, Waybill.STATUS_DEPARTED)
    wb.refresh_from_db()
    assert wb.loaded_at is not None
    assert wb.departed_at is not None
    assert wb.arrived_at is None


@pytest.mark.django_db
def test_manual_stop_event_endpoint(admin_client):
    wb = Waybill.objects.create(waybill_no="MANUAL1", route_name="r")
    WaybillStop.objects.create(waybill=wb, seq=1, stop_type="pickup", address="无坐标提货点")
    resp = admin_client.post("/api/v1/waybills/MANUAL1/stop-event", {"seq": 1, "event": "arrived"}, format="json")
    assert resp.status_code == 200, resp.content
    stop = wb.stops.get(seq=1)
    assert stop.actual_arrival_at is not None
    assert stop.arrival_source == WaybillStop.SRC_MANUAL


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="geo_admin", password="pw-strong-123456")
    client = APIClient()
    tok = client.post("/api/v1/auth/token", {"username": "geo_admin", "password": "pw-strong-123456"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return client
