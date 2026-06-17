"""指标定义：各主题域的统一口径计算（从 OLTP 库聚合）。

约定：计算函数签名 (start, end, dimension, filters)，返回 {"value": 数值,
可选 "breakdown": [{"key","value"}, ...]}。range 型按时间范围，snapshot 型取当前态。
"""

from datetime import date, timedelta
from decimal import Decimal

from django.db.models import Count, Sum
from django.utils import timezone
from django.utils.dateparse import parse_date

from .registry import (
    DOMAIN_FINANCE,
    DOMAIN_FLEET,
    DOMAIN_OPS,
    DOMAIN_ORDER,
    TYPE_SNAPSHOT,
    metric,
)


def _as_date(value, default):
    if value is None:
        return default
    if isinstance(value, date):
        return value
    return parse_date(str(value)) or default


def _range(start, end):
    # 用 localdate()（项目时区）与 created_at__date 等查询口径一致，避免跨 UTC/本地日界丢数据
    end_d = _as_date(end, timezone.localdate())
    start_d = _as_date(start, end_d - timedelta(days=30))
    return start_d, end_d


def _rate(num, den):
    return round(num / den, 4) if den else 0.0


def _breakdown(qs, dim):
    return [{"key": row[dim] or "未知", "value": row["c"]} for row in qs.values(dim).annotate(c=Count("id")).order_by("-c")]


# ── 运单 / 履约 ─────────────────────────────────────────
@metric("ops.waybill_count", "运单量", DOMAIN_OPS, unit="单", dimensions=["status", "risk_level"])
def waybill_count(*, start, end, dimension, filters):
    from apps.ops.models import Waybill

    s, e = _range(start, end)
    qs = Waybill.objects.filter(created_at__date__gte=s, created_at__date__lte=e)
    result = {"value": qs.count()}
    if dimension:
        result["breakdown"] = _breakdown(qs, dimension)
    return result


@metric("ops.in_transit", "在途运单", DOMAIN_OPS, unit="单", mtype=TYPE_SNAPSHOT)
def in_transit(*, start, end, dimension, filters):
    from apps.ops.models import Waybill

    return {"value": Waybill.objects.filter(status=Waybill.STATUS_IN_TRANSIT).count()}


@metric("ops.on_time_rate", "准时率", DOMAIN_OPS, unit="%", description="已送达运单中 ETA 未偏移占比")
def on_time_rate(*, start, end, dimension, filters):
    from apps.ops.models import Waybill

    s, e = _range(start, end)
    done = Waybill.objects.filter(
        status__in=[Waybill.STATUS_ARRIVED, Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED, Waybill.STATUS_SETTLED],
        created_at__date__gte=s, created_at__date__lte=e,
    )
    total = done.count()
    on_time = done.filter(eta_drift_minutes__lte=0).count()
    return {"value": _rate(on_time, total), "numerator": on_time, "denominator": total}


@metric("ops.risk_rate", "风险运单占比", DOMAIN_OPS, unit="%", mtype=TYPE_SNAPSHOT)
def risk_rate(*, start, end, dimension, filters):
    from apps.ops.models import Waybill

    active = Waybill.objects.exclude(status__in=[Waybill.STATUS_SETTLED, Waybill.STATUS_CANCELLED, Waybill.STATUS_VOIDED])
    total = active.count()
    risky = active.filter(risk_level__in=[Waybill.RISK_HIGH, Waybill.RISK_MEDIUM]).count()
    return {"value": _rate(risky, total), "numerator": risky, "denominator": total}


# ── 运力 / 车辆 ─────────────────────────────────────────
@metric("fleet.online_rate", "运力在线率", DOMAIN_FLEET, unit="%", mtype=TYPE_SNAPSHOT)
def online_rate(*, start, end, dimension, filters):
    from apps.telematics.models import VehicleState

    total = VehicleState.objects.count()
    online = VehicleState.objects.filter(online=True).count()
    return {"value": _rate(online, total), "numerator": online, "denominator": total}


@metric("fleet.utilization_rate", "运力利用率", DOMAIN_FLEET, unit="%", mtype=TYPE_SNAPSHOT,
        description="执行中运单占用车辆 / 在用车辆")
def utilization_rate(*, start, end, dimension, filters):
    from apps.masterdata.models import Vehicle
    from apps.ops.models import Waybill

    busy = set(
        Waybill.objects.filter(
            status__in=[Waybill.STATUS_DISPATCHED, Waybill.STATUS_LOADED, Waybill.STATUS_DEPARTED, Waybill.STATUS_IN_TRANSIT]
        ).exclude(vehicle__isnull=True).values_list("vehicle_id", flat=True)
    )
    total = Vehicle.objects.filter(is_active=True).count()
    return {"value": _rate(len(busy), total), "numerator": len(busy), "denominator": total}


@metric("fleet.alert_count", "报警数", DOMAIN_FLEET, unit="条", dimensions=["alert_type", "level"])
def alert_count(*, start, end, dimension, filters):
    from apps.telematics.models import Alert

    s, e = _range(start, end)
    qs = Alert.objects.filter(triggered_at__date__gte=s, triggered_at__date__lte=e)
    result = {"value": qs.count()}
    if dimension:
        result["breakdown"] = _breakdown(qs, dimension)
    return result


# ── 订单 / 渠道 ─────────────────────────────────────────
@metric("order.count", "订单量", DOMAIN_ORDER, unit="单", dimensions=["channel", "status"])
def order_count(*, start, end, dimension, filters):
    from apps.ops.models import Order

    s, e = _range(start, end)
    qs = Order.objects.filter(created_at__date__gte=s, created_at__date__lte=e)
    result = {"value": qs.count()}
    if dimension:
        result["breakdown"] = _breakdown(qs, dimension)
    return result


@metric("order.conversion_rate", "订单转化率", DOMAIN_ORDER, unit="%",
        description="转运单订单 / 订单总数")
def order_conversion_rate(*, start, end, dimension, filters):
    from apps.ops.models import Order

    s, e = _range(start, end)
    qs = Order.objects.filter(created_at__date__gte=s, created_at__date__lte=e)
    total = qs.count()
    converted = qs.filter(status=Order.STATUS_CONVERTED).count()
    return {"value": _rate(converted, total), "numerator": converted, "denominator": total}


# ── 财务 / 对账 ─────────────────────────────────────────
def _expense_total(direction, s, e):
    from apps.finance.models import ExpenseRecord

    return ExpenseRecord.objects.filter(
        direction=direction, occurred_at__date__gte=s, occurred_at__date__lte=e
    ).aggregate(t=Sum("amount"))["t"] or Decimal("0")


@metric("finance.receivable_total", "应收总额", DOMAIN_FINANCE, unit="元")
def receivable_total(*, start, end, dimension, filters):
    from apps.finance.models import ExpenseRecord

    s, e = _range(start, end)
    return {"value": float(_expense_total(ExpenseRecord.DIRECTION_RECEIVABLE, s, e))}


@metric("finance.payable_total", "应付总额", DOMAIN_FINANCE, unit="元")
def payable_total(*, start, end, dimension, filters):
    from apps.finance.models import ExpenseRecord

    s, e = _range(start, end)
    return {"value": float(_expense_total(ExpenseRecord.DIRECTION_PAYABLE, s, e))}


@metric("finance.statement_diff_total", "对账差异合计", DOMAIN_FINANCE, unit="元",
        description="对账单总额与对方金额差异之和（稽核）")
def statement_diff_total(*, start, end, dimension, filters):
    from apps.finance.models import Statement

    s, e = _range(start, end)
    agg = Statement.objects.filter(created_at__date__gte=s, created_at__date__lte=e).aggregate(
        total=Sum("total_amount"), external=Sum("external_total")
    )
    diff = (agg["total"] or Decimal("0")) - (agg["external"] or Decimal("0"))
    return {"value": float(diff)}
