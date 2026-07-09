"""ETA 预测引擎：基于当前定位 + 剩余里程 + 均速动态预测到达时间与偏移。

替代此前静态写死的 estimated_arrival/eta_drift_minutes——按运单最新轨迹点到
目的地（末个送货点坐标）的球面距离、近段实测均速（无则默认巡航速度）推算，
叠加道路系数（直线→实际里程）与装卸缓冲，并回写运单与超时偏移。
"""

from datetime import timedelta

from django.utils import timezone

from .geofence import haversine_m
from .models import Waybill, WaybillStop

# 直线距离 → 实际路网里程的经验系数
ROAD_FACTOR = 1.3
# 无实测速度时的默认巡航速度（km/h）
DEFAULT_SPEED_KMH = 55.0
# 有效行驶速度下限（低于此视为停车/拥堵，不纳入均速）
MIN_MOVING_KMH = 5.0


def _avg_speed(points) -> float:
    speeds = [float(p.speed_kmh) for p in points if p.speed_kmh and float(p.speed_kmh) >= MIN_MOVING_KMH]
    return sum(speeds) / len(speeds) if speeds else DEFAULT_SPEED_KMH


def predict_eta(waybill, *, now=None, persist=True) -> dict | None:
    """预测运单 ETA。数据不足（无轨迹点或无目的地坐标）返回 None。

    返回 {estimated_arrival, eta_drift_minutes, remaining_km, avg_speed_kmh}，
    并（默认）回写 waybill.estimated_arrival / eta_drift_minutes。
    """
    now = now or timezone.now()
    latest = waybill.tracking_points.order_by("-reported_at").first()
    dest = (
        waybill.stops.filter(
            stop_type=WaybillStop.STOP_DELIVERY, lat__isnull=False, lng__isnull=False
        )
        .order_by("-seq")
        .first()
    )
    if latest is None or dest is None or dest.lat is None or dest.lng is None:
        return None

    remaining_km = haversine_m(latest.lat, latest.lng, dest.lat, dest.lng) / 1000.0 * ROAD_FACTOR
    avg_speed = _avg_speed(list(waybill.tracking_points.order_by("-reported_at")[:5]))
    eta_minutes = (remaining_km / avg_speed) * 60 if avg_speed else 0
    estimated = now + timedelta(minutes=eta_minutes)
    drift = 0
    if waybill.planned_arrival:
        drift = int((estimated - waybill.planned_arrival).total_seconds() // 60)

    if persist:
        waybill.estimated_arrival = estimated
        waybill.eta_drift_minutes = drift
        waybill.save(update_fields=["estimated_arrival", "eta_drift_minutes", "updated_at"])

    return {
        "waybill_no": waybill.waybill_no,
        "estimated_arrival": estimated,
        "planned_arrival": waybill.planned_arrival,
        "eta_drift_minutes": drift,
        "remaining_km": round(remaining_km, 1),
        "avg_speed_kmh": round(avg_speed, 1),
        "predicted": True,
    }


def refresh_all_in_transit_eta() -> int:
    """批量刷新所有在途运单 ETA（供定时任务调用）。返回成功预测的运单数。"""
    count = 0
    for wb in Waybill.objects.filter(status=Waybill.STATUS_IN_TRANSIT):
        if predict_eta(wb) is not None:
            count += 1
    return count
