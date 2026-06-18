"""点位到达自动化：GPS 围栏判定 + 实际到达/离开时间盖戳。

轨迹点进入点位围栏半径 → 盖 actual_arrival_at；离开 → 盖 actual_depart_at。
由轨迹削峰落库任务调用，离线可测、与状态流转解耦。
"""

import math

from django.utils import timezone

from apps.core.redis import publish_event

from .models import WaybillEvent, WaybillStop

EARTH_RADIUS_M = 6371000.0


def haversine_m(lat1, lng1, lat2, lng2) -> float:
    """两经纬度间球面距离（米）。"""
    p1, p2 = math.radians(float(lat1)), math.radians(float(lat2))
    dp = math.radians(float(lat2) - float(lat1))
    dl = math.radians(float(lng2) - float(lng1))
    a = math.sin(dp / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(dl / 2) ** 2
    return 2 * EARTH_RADIUS_M * math.asin(math.sqrt(a))


def _record(stop, event_type, ts):
    WaybillEvent.objects.create(
        waybill_id=stop.waybill_id,
        event_type=event_type,
        event_time=ts,
        resource=f"stop#{stop.seq}",
        source="geofence",
        payload={"seq": stop.seq, "stop_type": stop.stop_type, "address": stop.address},
    )


def process_point(waybill, lat, lng, ts=None) -> list[dict]:
    """对单个轨迹点做围栏判定，盖到达/离开戳。返回发生的变更列表。"""
    ts = ts or timezone.now()
    changes = []
    stops = WaybillStop.objects.filter(waybill=waybill, lat__isnull=False, lng__isnull=False).order_by("seq")
    for stop in stops:
        dist = haversine_m(lat, lng, stop.lat, stop.lng)
        inside = dist <= float(stop.radius_m)
        if inside and stop.actual_arrival_at is None:
            stop.actual_arrival_at = ts
            stop.arrival_source = WaybillStop.SRC_GPS
            stop.status = WaybillStop.STATUS_ARRIVED
            stop.save(update_fields=["actual_arrival_at", "arrival_source", "status", "updated_at"])
            _record(stop, "stop_arrived", ts)
            publish_event("stop_arrived", {"waybill_no": waybill.waybill_no, "seq": stop.seq, "address": stop.address})
            changes.append({"seq": stop.seq, "event": "arrived"})
        elif not inside and stop.actual_arrival_at is not None and stop.actual_depart_at is None:
            stop.actual_depart_at = ts
            stop.status = WaybillStop.STATUS_DEPARTED
            stop.save(update_fields=["actual_depart_at", "status", "updated_at"])
            _record(stop, "stop_departed", ts)
            publish_event("stop_departed", {"waybill_no": waybill.waybill_no, "seq": stop.seq})
            changes.append({"seq": stop.seq, "event": "departed"})
    return changes


def process_points(points) -> int:
    """批量处理（轨迹落库后调用）：points 为 [(waybill, lat, lng, ts), ...]，按时间序处理。"""
    total = 0
    for waybill, lat, lng, ts in sorted(points, key=lambda x: x[3] or timezone.now()):
        total += len(process_point(waybill, lat, lng, ts))
    return total
