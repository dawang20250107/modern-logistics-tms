"""车联网监控与报警中心测试。"""

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.masterdata.models import Carrier, Vehicle
from apps.ops.models import TrackingPoint, Waybill
from apps.telematics.models import Alert, Device, VehicleState
from apps.telematics.services import evaluate_telemetry, persist_reports, raise_alert, scan_offline_devices


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


@pytest.mark.django_db
def test_live_vehicles_endpoint(admin_client):
    vehicle = Vehicle.objects.create(plate_no="沪E0001")
    VehicleState.objects.create(vehicle=vehicle, online=True, lat=31.2, lng=121.4, reported_at=timezone.now())

    resp = admin_client.get("/api/v1/telematics/vehicles/live?online=true")
    assert resp.status_code == 200, resp.content
    vehicles = resp.json()["data"]["vehicles"]
    assert len(vehicles) == 1
    assert vehicles[0]["vehicle_plate"] == "沪E0001"
