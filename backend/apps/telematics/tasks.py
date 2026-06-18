"""车联网上报削峰落库与离线扫描的异步任务。"""

import json

from celery import shared_task

from apps.core.redis import get_redis

from .services import persist_reports, scan_offline_devices

TELEMETRY_QUEUE = "tms:telemetry:queue"


@shared_task(name="telematics.flush_telemetry")
def flush_telemetry(batch: int = 1000) -> int:
    """从 Redis 队列批量取出设备上报并落库 + 触发报警（削峰）。"""
    r = get_redis()
    items = r.lpop(TELEMETRY_QUEUE, batch)
    if not items:
        return 0
    if isinstance(items, str):
        items = [items]
    parsed = [json.loads(i) for i in items]
    counts = persist_reports(parsed)
    return counts["states"] + counts["points"] + counts["alerts"]


@shared_task(name="telematics.scan_offline_devices")
def scan_offline_devices_task() -> int:
    """周期扫描掉线设备并报警。"""
    return scan_offline_devices()
