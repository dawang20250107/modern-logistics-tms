"""指标服务：经营看板装配、单日物化、趋势查询。"""

from datetime import timedelta

from django.utils import timezone

from .models import MetricSnapshot
from .registry import compute_metric, list_metrics

# 经营看板默认指标集（一屏经营/运营态势）
DASHBOARD_METRICS = [
    "ops.waybill_count",
    "ops.in_transit",
    "ops.on_time_rate",
    "ops.risk_rate",
    "fleet.online_rate",
    "fleet.utilization_rate",
    "fleet.alert_count",
    "order.count",
    "order.conversion_rate",
    "finance.receivable_total",
    "finance.payable_total",
    "finance.statement_diff_total",
]

# 需按日物化的指标（用于趋势）
MATERIALIZE_METRICS = [
    "ops.waybill_count",
    "fleet.alert_count",
    "order.count",
    "finance.receivable_total",
    "finance.payable_total",
]


def build_dashboard(start=None, end=None) -> dict:
    cards = []
    for code in DASHBOARD_METRICS:
        try:
            cards.append(compute_metric(code, start=start, end=end))
        except Exception:  # noqa: BLE001 - 单指标异常不拖垮整盘
            continue
    return {"metrics": cards}


def materialize_daily(target_date=None) -> int:
    """把当日指标值落快照（幂等 upsert）。返回写入指标数。"""
    day = target_date or timezone.now().date()
    count = 0
    for code in MATERIALIZE_METRICS:
        result = compute_metric(code, start=day, end=day)
        MetricSnapshot.objects.update_or_create(
            metric_code=code, stat_date=day, dimension_key="",
            defaults={"value": result["value"]},
        )
        count += 1
    return count


def metric_trend(code: str, days: int = 14) -> dict:
    end = timezone.now().date()
    start = end - timedelta(days=days)
    rows = (
        MetricSnapshot.objects.filter(metric_code=code, dimension_key="", stat_date__gte=start, stat_date__lte=end)
        .order_by("stat_date")
        .values("stat_date", "value")
    )
    spec = next((m for m in list_metrics() if m["code"] == code), None)
    return {
        "code": code,
        "name": spec["name"] if spec else code,
        "unit": spec["unit"] if spec else "",
        "series": [{"date": r["stat_date"].isoformat(), "value": float(r["value"])} for r in rows],
    }
