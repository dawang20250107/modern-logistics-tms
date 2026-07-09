"""ETA 预测引擎 + 真实准班率（planned vs arrived 时间戳）。"""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.ops.eta import predict_eta
from apps.ops.models import TrackingPoint, Waybill, WaybillStop


def _wb_with_route(planned_offset_min=600):
    """建一条在途运单：当前在 (31.0,121.0)，目的地 (31.9,121.0)，约 100km。"""
    wb = Waybill.objects.create(
        waybill_no=f"ETA{Waybill.objects.count()+1}", route_name="r",
        status=Waybill.STATUS_IN_TRANSIT,
        planned_arrival=timezone.now() + timedelta(minutes=planned_offset_min),
    )
    WaybillStop.objects.create(waybill=wb, seq=2, stop_type=WaybillStop.STOP_DELIVERY, lat="31.9", lng="121.0")
    return wb


@pytest.mark.django_db
def test_predict_eta_from_position_and_speed():
    wb = _wb_with_route()
    now = timezone.now()
    TrackingPoint.objects.create(waybill=wb, lat="31.0", lng="121.0", speed_kmh="60", reported_at=now)

    result = predict_eta(wb, now=now)
    assert result is not None
    # 约 100km 直线 ×1.3 道路系数 ≈ 130km；60km/h ≈ 130 分钟
    assert 120 <= result["remaining_km"] <= 140
    assert result["avg_speed_kmh"] == 60.0
    wb.refresh_from_db()
    assert wb.estimated_arrival is not None
    # 计划 600 分钟后到，预计约 130 分钟到 → 提前，drift 为负
    assert wb.eta_drift_minutes < 0


@pytest.mark.django_db
def test_predict_eta_default_speed_when_no_moving_points():
    wb = _wb_with_route()
    now = timezone.now()
    # 停车点（速度 0）→ 均速回退默认巡航速度
    TrackingPoint.objects.create(waybill=wb, lat="31.0", lng="121.0", speed_kmh="0", reported_at=now)
    result = predict_eta(wb, now=now)
    assert result["avg_speed_kmh"] == 55.0  # DEFAULT_SPEED_KMH


@pytest.mark.django_db
def test_predict_eta_none_without_data():
    wb = Waybill.objects.create(waybill_no="ETANIL", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    # 无轨迹点、无目的地坐标 → 无法预测
    assert predict_eta(wb) is None


@pytest.mark.django_db
def test_late_arrival_lowers_on_time_rate():
    from apps.analytics.registry import compute_metric

    base = timezone.now() - timedelta(days=1)
    # 准点：实际到达早于计划
    Waybill.objects.create(waybill_no="OT1", route_name="r", status=Waybill.STATUS_SIGNED,
                           planned_arrival=base + timedelta(hours=2), arrived_at=base + timedelta(hours=1))
    # 迟到：实际到达晚于计划
    Waybill.objects.create(waybill_no="OT2", route_name="r", status=Waybill.STATUS_SIGNED,
                           planned_arrival=base + timedelta(hours=1), arrived_at=base + timedelta(hours=3))
    # 无时间戳：不纳入分母
    Waybill.objects.create(waybill_no="OT3", route_name="r", status=Waybill.STATUS_SIGNED)

    result = compute_metric("ops.on_time_rate")
    assert result["denominator"] == 2  # 仅两条有双时间戳
    assert result["numerator"] == 1
    assert result["value"] == 0.5


@pytest.mark.django_db
def test_eta_endpoint(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="eta_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "eta_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")

    wb = _wb_with_route()
    TrackingPoint.objects.create(waybill=wb, lat="31.0", lng="121.0", speed_kmh="70", reported_at=timezone.now())
    r = c.get(f"/api/v1/waybills/{wb.waybill_no}/eta")
    assert r.status_code == 200
    data = r.json()["data"]
    assert data["predicted"] is True
    assert data["remaining_km"] > 100
    assert data["avg_speed_kmh"] == 70.0
