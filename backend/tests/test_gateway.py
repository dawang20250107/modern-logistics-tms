"""IoT 终端网关（JT/T 808 + 归一化 + 入队）测试。"""

import pytest

from apps.masterdata.models import Vehicle
from apps.telematics.gateway import (
    build_jt808_location,
    ingest_terminal_report,
    normalize_terminal_message,
    parse_jt808,
)
from apps.telematics.models import Device, VehicleState


def test_jt808_build_parse_roundtrip():
    frame = build_jt808_location("130000000001", lng=121.473700, lat=31.230400, speed_kmh=72.0, direction=90)
    parsed = parse_jt808(frame)
    assert parsed["msg_id"] == 0x0200
    assert round(parsed["lng"], 6) == 121.473700
    assert round(parsed["lat"], 6) == 31.230400
    assert parsed["speed_kmh"] == 72.0
    assert parsed["direction"] == 90
    assert parsed["terminal_phone"] == "130000000001"


def test_jt808_checksum_validation():
    frame = bytearray(build_jt808_location("130000000001", 121.0, 31.0))
    frame[5] ^= 0xFF  # 破坏一个字节
    with pytest.raises(ValueError):
        parse_jt808(bytes(frame))


def test_normalize_jt808_and_json():
    frame = build_jt808_location("130000000001", lng=121.5, lat=31.2, speed_kmh=60)
    report = normalize_terminal_message(frame)
    assert report["provider"] == "jt808"
    assert report["device_no"] == "130000000001"
    assert round(report["lng"], 6) == 121.5
    assert report["reported_at"].startswith("2026-06-01T08:00:00")

    json_report = normalize_terminal_message({"vehicle_plate": "沪A1", "lng": 120, "lat": 30}, device_no="D9")
    assert json_report["provider"] == "mqtt"
    assert json_report["device_no"] == "D9"


@pytest.mark.django_db(transaction=True)
def test_ingest_terminal_report_persists_via_queue():
    vehicle = Vehicle.objects.create(plate_no="沪J0001")
    Device.objects.create(device_no="130000000001", vehicle=vehicle)
    frame = build_jt808_location("130000000001", lng=121.47, lat=31.23, speed_kmh=50)

    report = normalize_terminal_message(frame)
    assert ingest_terminal_report(report) is True  # 入队 + flush（eager）

    state = VehicleState.objects.get(vehicle=vehicle)
    assert round(float(state.lng), 2) == 121.47
    assert state.online is True
