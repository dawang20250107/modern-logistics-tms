"""报警规则引擎与上报落库（与 Redis/Celery 解耦，便于直接测试）。

evaluate_telemetry：纯函数，根据单条上报算出应触发的报警；
persist_reports：批量落库 + 更新车辆实时状态 + 轨迹续点 + 触发报警（供 flush 任务复用）。
"""

from datetime import timedelta

from django.conf import settings
from django.utils import timezone
from django.utils.dateparse import parse_datetime

from apps.core.redis import publish_event

from .models import Alert, Device, VehicleState

# 设备上报事件码 → (报警类型, 等级)
_EVENT_ALERT_MAP = {
    "fatigue": (Alert.TYPE_FATIGUE, Alert.LEVEL_HIGH),
    "abnormal_stop": (Alert.TYPE_ABNORMAL_STOP, Alert.LEVEL_MEDIUM),
    "deviation": (Alert.TYPE_DEVIATION, Alert.LEVEL_MEDIUM),
}


def _f(value):
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def evaluate_telemetry(report: dict) -> list[dict]:
    """根据单条上报算出应触发的报警列表（不落库）。"""
    alerts: list[dict] = []

    speed = _f(report.get("speed_kmh"))
    limit = settings.ALERT_SPEED_LIMIT_KMH
    if speed is not None and speed > limit:
        high = speed > limit + settings.ALERT_SPEED_HIGH_MARGIN
        alerts.append(
            {
                "alert_type": Alert.TYPE_OVERSPEED,
                "level": Alert.LEVEL_HIGH if high else Alert.LEVEL_MEDIUM,
                "message": f"超速 {speed:.0f} km/h（限速 {limit:.0f}）",
                "value": speed,
                "threshold": limit,
            }
        )

    temp = _f(report.get("temperature_c"))
    if temp is not None and (temp < settings.ALERT_TEMP_MIN_C or temp > settings.ALERT_TEMP_MAX_C):
        alerts.append(
            {
                "alert_type": Alert.TYPE_TEMPERATURE,
                "level": Alert.LEVEL_HIGH,
                "message": f"温度异常 {temp:.1f}℃（允许 {settings.ALERT_TEMP_MIN_C}~{settings.ALERT_TEMP_MAX_C}℃）",
                "value": temp,
                "threshold": settings.ALERT_TEMP_MAX_C,
            }
        )

    fuel = _f(report.get("fuel_pct"))
    if fuel is not None and fuel < settings.ALERT_FUEL_LOW_PCT:
        alerts.append(
            {
                "alert_type": Alert.TYPE_FUEL,
                "level": Alert.LEVEL_MEDIUM,
                "message": f"油量偏低 {fuel:.0f}%（阈值 {settings.ALERT_FUEL_LOW_PCT}%）",
                "value": fuel,
                "threshold": settings.ALERT_FUEL_LOW_PCT,
            }
        )

    for code in report.get("events", []) or []:
        mapped = _EVENT_ALERT_MAP.get(code)
        if mapped:
            alert_type, level = mapped
            alerts.append(
                {"alert_type": alert_type, "level": level, "message": f"设备事件：{code}", "detail": {"event": code}}
            )

    return alerts


def _recent_open_alert_exists(vehicle, alert_type: str) -> bool:
    if vehicle is None:
        return False
    since = timezone.now() - timedelta(minutes=settings.ALERT_DEDUP_MINUTES)
    return Alert.objects.filter(
        vehicle=vehicle, alert_type=alert_type, status=Alert.STATUS_OPEN, triggered_at__gte=since
    ).exists()


def raise_alert(spec: dict, *, vehicle=None, device=None, waybill=None, triggered_at=None, dedup=True) -> Alert | None:
    """创建报警并推送实时事件；同车同类型在去重窗口内已有未处理报警则跳过。"""
    alert_type = spec["alert_type"]
    if dedup and _recent_open_alert_exists(vehicle, alert_type):
        return None
    alert = Alert.objects.create(
        alert_type=alert_type,
        level=spec.get("level", Alert.LEVEL_MEDIUM),
        vehicle=vehicle,
        device=device,
        waybill=waybill,
        message=spec.get("message", ""),
        value=spec.get("value"),
        threshold=spec.get("threshold"),
        detail=spec.get("detail", {}),
        triggered_at=triggered_at or timezone.now(),
    )
    publish_event(
        "alert",
        {
            "id": str(alert.id),
            "alert_type": alert.alert_type,
            "level": alert.level,
            "message": alert.message,
            "vehicle_id": str(vehicle.id) if vehicle else None,
            "waybill_no": waybill.waybill_no if waybill else None,
        },
    )
    return alert


def persist_reports(parsed: list[dict]) -> dict:
    """批量处理上报：更新设备/车辆实时状态、续轨迹点、触发报警。返回各项计数。"""
    from apps.masterdata.models import Vehicle
    from apps.ops.models import TrackingPoint, Waybill

    plates = {r.get("vehicle_plate") for r in parsed if r.get("vehicle_plate")}
    device_nos = {r.get("device_no") for r in parsed if r.get("device_no")}
    wbnos = {r.get("waybill_no") for r in parsed if r.get("waybill_no")}

    vehicles = {v.plate_no: v for v in Vehicle.objects.filter(plate_no__in=plates)}
    devices = {d.device_no: d for d in Device.objects.filter(device_no__in=device_nos)}
    waybills = {w.waybill_no: w for w in Waybill.objects.filter(waybill_no__in=wbnos)}

    counts = {"states": 0, "points": 0, "alerts": 0, "devices": 0}
    track_objs = []

    for report in parsed:
        reported_at = parse_datetime(report.get("reported_at") or "") or timezone.now()
        device = devices.get(report.get("device_no"))
        waybill = waybills.get(report.get("waybill_no"))
        vehicle = vehicles.get(report.get("vehicle_plate")) or (device.vehicle if device else None)

        if device is not None:
            device.last_seen_at = reported_at
            device.status = Device.STATUS_ONLINE
            device.save(update_fields=["last_seen_at", "status", "updated_at"])
            counts["devices"] += 1

        if vehicle is not None:
            VehicleState.objects.update_or_create(
                vehicle=vehicle,
                defaults={
                    "waybill": waybill,
                    "lng": report.get("lng") or 0,
                    "lat": report.get("lat") or 0,
                    "speed_kmh": report.get("speed_kmh") or 0,
                    "heading": report.get("heading") or 0,
                    "mileage_km": report.get("mileage_km") or 0,
                    "temperature_c": report.get("temperature_c"),
                    "fuel_pct": report.get("fuel_pct"),
                    "online": True,
                    "reported_at": reported_at,
                },
            )
            counts["states"] += 1

        if waybill is not None and report.get("lng") is not None and report.get("lat") is not None:
            track_objs.append(
                TrackingPoint(
                    waybill=waybill,
                    lng=report.get("lng"),
                    lat=report.get("lat"),
                    speed_kmh=report.get("speed_kmh") or 0,
                    reported_at=reported_at,
                    provider=report.get("provider", ""),
                )
            )

        for spec in evaluate_telemetry(report):
            if raise_alert(spec, vehicle=vehicle, device=device, waybill=waybill, triggered_at=reported_at):
                counts["alerts"] += 1

    if track_objs:
        TrackingPoint.objects.bulk_create(track_objs, batch_size=500)
        counts["points"] = len(track_objs)

    return counts


def scan_offline_devices() -> int:
    """把超时未上报的设备/车辆标记为离线并报警。返回新置离线设备数。"""
    threshold = timezone.now() - timedelta(minutes=settings.DEVICE_OFFLINE_MINUTES)
    stale = Device.objects.filter(status=Device.STATUS_ONLINE).filter(
        last_seen_at__lt=threshold
    ) | Device.objects.filter(status=Device.STATUS_ONLINE, last_seen_at__isnull=True)
    count = 0
    for device in stale.select_related("vehicle"):
        device.status = Device.STATUS_OFFLINE
        device.save(update_fields=["status", "updated_at"])
        if device.vehicle_id:
            VehicleState.objects.filter(vehicle=device.vehicle).update(online=False)
        raise_alert(
            {
                "alert_type": Alert.TYPE_OFFLINE,
                "level": Alert.LEVEL_MEDIUM,
                "message": f"设备 {device.device_no} 超过 {settings.DEVICE_OFFLINE_MINUTES} 分钟未上报",
            },
            vehicle=device.vehicle,
            device=device,
        )
        count += 1
    return count
