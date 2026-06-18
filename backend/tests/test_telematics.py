"""车联网监控与报警中心测试。"""

from datetime import UTC

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.masterdata.models import Carrier, Vehicle
from apps.ops.models import TrackingPoint, Waybill
from apps.telematics.geo import analyze_trajectory, distance_to_polyline_m, point_in_circle, point_in_polygon
from apps.telematics.models import Alert, Device, Geofence, VehicleState
from apps.telematics.services import (
    evaluate_deviation,
    evaluate_geofences,
    evaluate_telemetry,
    persist_reports,
    raise_alert,
    scan_offline_devices,
)


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def test_evaluate_telemetry_overspeed_and_temperature():
    alerts = evaluate_telemetry({"speed_kmh": 130, "temperature_c": 20, "fuel_pct": 50})
    types = {a["alert_type"] for a in alerts}
    assert Alert.TYPE_OVERSPEED in types
    assert Alert.TYPE_TEMPERATURE in types
    overspeed = next(a for a in alerts if a["alert_type"] == Alert.TYPE_OVERSPEED)
    assert overspeed["level"] == Alert.LEVEL_HIGH  # 130 > 90 + 20


def test_evaluate_telemetry_quiet_when_normal():
    assert evaluate_telemetry({"speed_kmh": 60, "temperature_c": 2, "fuel_pct": 80}) == []


@pytest.mark.django_db
def test_persist_reports_updates_state_and_raises_alerts():
    carrier = Carrier.objects.create(code="C1", name="承运A")
    vehicle = Vehicle.objects.create(plate_no="沪A12345", carrier=carrier)
    Device.objects.create(device_no="D1", device_type=Device.TYPE_GPS, vehicle=vehicle)
    wb = Waybill.objects.create(waybill_no="WBT1", route_name="沪-蓉", vehicle=vehicle)

    counts = persist_reports([
        {
            "device_no": "D1",
            "vehicle_plate": "沪A12345",
            "waybill_no": "WBT1",
            "lng": 121.47,
            "lat": 31.23,
            "speed_kmh": 130,
            "temperature_c": 20,
            "reported_at": timezone.now().isoformat(),
        }
    ])

    assert counts["states"] == 1
    assert counts["points"] == 1
    assert counts["alerts"] == 2  # 超速 + 温度

    state = VehicleState.objects.get(vehicle=vehicle)
    assert state.online is True
    assert TrackingPoint.objects.filter(waybill=wb).count() == 1
    assert Device.objects.get(device_no="D1").status == Device.STATUS_ONLINE
    assert Alert.objects.filter(vehicle=vehicle, alert_type=Alert.TYPE_OVERSPEED).count() == 1


@pytest.mark.django_db
def test_alert_dedup_within_window():
    vehicle = Vehicle.objects.create(plate_no="沪B0001")
    spec = {"alert_type": Alert.TYPE_OVERSPEED, "level": Alert.LEVEL_MEDIUM, "message": "超速"}
    assert raise_alert(spec, vehicle=vehicle) is not None
    assert raise_alert(spec, vehicle=vehicle) is None  # 去重窗口内不重复
    assert Alert.objects.filter(vehicle=vehicle, alert_type=Alert.TYPE_OVERSPEED).count() == 1


@pytest.mark.django_db
def test_scan_offline_devices_marks_offline_and_alerts():
    vehicle = Vehicle.objects.create(plate_no="沪C0001")
    VehicleState.objects.create(vehicle=vehicle, online=True, reported_at=timezone.now())
    Device.objects.create(
        device_no="D-OLD",
        vehicle=vehicle,
        status=Device.STATUS_ONLINE,
        last_seen_at=timezone.now() - timezone.timedelta(hours=2),
    )

    count = scan_offline_devices()

    assert count == 1
    assert Device.objects.get(device_no="D-OLD").status == Device.STATUS_OFFLINE
    assert VehicleState.objects.get(vehicle=vehicle).online is False
    assert Alert.objects.filter(alert_type=Alert.TYPE_OFFLINE, vehicle=vehicle).count() == 1


@pytest.mark.django_db
def test_alert_ack_and_close_endpoint(admin_client):
    vehicle = Vehicle.objects.create(plate_no="沪D0001")
    alert = Alert.objects.create(
        alert_type=Alert.TYPE_OVERSPEED, message="超速", vehicle=vehicle, triggered_at=timezone.now()
    )

    resp = admin_client.post(f"/api/v1/telematics/alerts/{alert.id}/ack")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["status"] == Alert.STATUS_ACK

    resp = admin_client.post(f"/api/v1/telematics/alerts/{alert.id}/close")
    assert resp.json()["data"]["status"] == Alert.STATUS_CLOSED
    alert.refresh_from_db()
    assert alert.handled_at is not None


@pytest.mark.django_db
def test_ai_vehicle_alert_summary_tool():
    from apps.ai.services.tools import execute_tool

    vehicle = Vehicle.objects.create(plate_no="沪F0001")
    wb = Waybill.objects.create(waybill_no="WBT9", route_name="沪-蓉", vehicle=vehicle)
    Alert.objects.create(
        alert_type=Alert.TYPE_OVERSPEED, level=Alert.LEVEL_HIGH, vehicle=vehicle, waybill=wb,
        message="超速", triggered_at=timezone.now(),
    )

    result = execute_tool("telematics.vehicle_alert_summary", {"waybill_no": "WBT9"})

    assert result["risk_detected"] is True
    assert result["evidence"]["open_alert_count"] == 1
    assert result["suggestion"] is not None  # 高危报警生成待确认建议


def test_geo_point_in_circle_and_polygon():
    # 上海人民广场附近
    assert point_in_circle(121.4737, 31.2304, 121.4737, 31.2304, 500) is True
    assert point_in_circle(121.60, 31.40, 121.4737, 31.2304, 500) is False
    square = [[0, 0], [0, 10], [10, 10], [10, 0]]
    assert point_in_polygon(5, 5, square) is True
    assert point_in_polygon(15, 5, square) is False


def test_geo_distance_to_polyline():
    line = [[0, 0], [0, 1]]  # 沿经线
    d = distance_to_polyline_m(0.01, 0.5, line)
    assert 1000 < d < 1300  # ~0.01° 经度 ≈ 1.1km


def test_analyze_trajectory_detects_stop_and_overspeed():
    from datetime import datetime, timedelta

    base = datetime(2026, 6, 1, 8, 0, tzinfo=UTC)
    points = [
        {"lng": 121.0, "lat": 31.0, "speed_kmh": 0, "reported_at": base},
        {"lng": 121.0001, "lat": 31.0001, "speed_kmh": 0, "reported_at": base + timedelta(minutes=15)},
        {"lng": 121.5, "lat": 31.2, "speed_kmh": 120, "reported_at": base + timedelta(minutes=40)},
        {"lng": 121.6, "lat": 31.3, "speed_kmh": 130, "reported_at": base + timedelta(minutes=50)},
        {"lng": 121.7, "lat": 31.4, "speed_kmh": 60, "reported_at": base + timedelta(minutes=60)},
    ]
    result = analyze_trajectory(points, speed_limit=90)
    assert len(result["stops"]) == 1  # 前两点同地停留 15min
    assert result["stops"][0]["duration_seconds"] == 900
    assert len(result["overspeed_segments"]) == 1  # 中间连续两点超速
    assert result["overspeed_segments"][0]["max_speed"] == 130


@pytest.mark.django_db
def test_geofence_enter_exit_raises_alerts():
    vehicle = Vehicle.objects.create(plate_no="沪G0001")
    fence = Geofence.objects.create(
        name="上海仓", shape=Geofence.SHAPE_CIRCLE, center_lng=121.0, center_lat=31.0, radius_m=1000
    )
    now = timezone.now()
    # 首次在外（建立初始状态，不报警）
    evaluate_geofences(vehicle, 122.0, 32.0, None, now)
    assert Alert.objects.filter(alert_type=Alert.TYPE_GEOFENCE).count() == 0
    # 进入围栏 → 报警
    evaluate_geofences(vehicle, 121.0005, 31.0005, None, now, [fence])
    # 离开围栏 → 报警
    evaluate_geofences(vehicle, 122.0, 32.0, None, now, [fence])
    alerts = Alert.objects.filter(alert_type=Alert.TYPE_GEOFENCE).order_by("created_at")
    assert alerts.count() == 2
    assert alerts.first().detail["action"] == "enter"
    assert alerts.last().detail["action"] == "exit"


@pytest.mark.django_db
def test_deviation_alert_when_off_route():
    from apps.masterdata.models import Route

    vehicle = Vehicle.objects.create(plate_no="沪H0001")
    route = Route.objects.create(
        code="R1", name="沪-蓉", waypoints=[[121.0, 31.0], [121.0, 31.5]], corridor_m=2000
    )
    wb = Waybill.objects.create(waybill_no="WBDEV", route_name="沪-蓉", vehicle=vehicle, planned_route=route)
    now = timezone.now()
    # 在走廊内 → 不报警
    assert evaluate_deviation(vehicle, 121.001, 31.2, wb, now) == 0
    # 明显偏离（经度偏 0.1° ≈ 9km）→ 报警
    assert evaluate_deviation(vehicle, 121.1, 31.2, wb, now) == 1
    assert Alert.objects.filter(alert_type=Alert.TYPE_DEVIATION, waybill=wb).count() == 1


@pytest.mark.django_db
def test_route_crud_and_expiring_credentials(admin_client):
    from datetime import date, timedelta

    resp = admin_client.post(
        "/api/v1/routes",
        {"code": "R9", "name": "沪-蓉", "waypoints": [[121, 31], [104, 30]], "corridor_m": "2000"},
        format="json",
    )
    assert resp.status_code == 201, resp.content

    Vehicle.objects.create(plate_no="沪Z9999", inspection_expiry=date.today() + timedelta(days=10))
    resp = admin_client.get("/api/v1/credentials/expiring?days=30")
    assert resp.status_code == 200, resp.content
    plates = [v["plate_no"] for v in resp.json()["data"]["vehicles"]]
    assert "沪Z9999" in plates


@pytest.mark.django_db
def test_trajectory_endpoint(admin_client):
    from datetime import datetime, timedelta

    wb = Waybill.objects.create(waybill_no="WBTRAJ", route_name="沪-蓉")
    base = datetime(2026, 6, 1, 8, 0, tzinfo=UTC)
    for i, sp in enumerate([0, 120, 130, 60]):
        TrackingPoint.objects.create(
            waybill=wb, lng=121 + i * 0.1, lat=31 + i * 0.1, speed_kmh=sp,
            reported_at=base + timedelta(minutes=i * 10),
        )
    resp = admin_client.get("/api/v1/telematics/waybills/WBTRAJ/trajectory?speed_limit=90")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["total_points"] == 4
    assert len(data["overspeed_segments"]) == 1


@pytest.mark.django_db
def test_command_center_summary(admin_client):
    v = Vehicle.objects.create(plate_no="沪K0001")
    VehicleState.objects.create(vehicle=v, online=True, reported_at=timezone.now())
    Waybill.objects.create(waybill_no="CC1", route_name="r", status=Waybill.STATUS_PENDING_DISPATCH)
    Alert.objects.create(alert_type=Alert.TYPE_OVERSPEED, level=Alert.LEVEL_HIGH, message="超速", triggered_at=timezone.now())

    resp = admin_client.get("/api/v1/telematics/command-center/summary")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["online_vehicles"] == 1
    assert data["pending_dispatch"] == 1
    assert data["open_alerts"] == 1
    assert data["high_alerts"] == 1


@pytest.mark.django_db
def test_live_vehicles_endpoint(admin_client):
    vehicle = Vehicle.objects.create(plate_no="沪E0001")
    VehicleState.objects.create(vehicle=vehicle, online=True, lat=31.2, lng=121.4, reported_at=timezone.now())

    resp = admin_client.get("/api/v1/telematics/vehicles/live?online=true")
    assert resp.status_code == 200, resp.content
    vehicles = resp.json()["data"]["vehicles"]
    assert len(vehicles) == 1
    assert vehicles[0]["vehicle_plate"] == "沪E0001"
