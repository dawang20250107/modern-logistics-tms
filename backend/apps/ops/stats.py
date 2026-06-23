"""主数据派生统计：司机累计运单/运费、车辆历史运费、客户历史运单/常用线路。

司机累计为存储字段（运单签收/合同确认时刷新）；车辆/客户为按需聚合（读时计算）。
"""

from collections import Counter

from django.db.models import Sum

from .models import Waybill

# 视为"已完成"参与累计的运单状态
_DONE_STATUSES = [Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED, Waybill.STATUS_SETTLED]


def refresh_driver_stats(driver) -> None:
    """刷新司机累计运单数与累计运费（运单签收/合同确认后调用）。"""
    if driver is None:
        return
    from apps.finance.models import ExpenseRecord

    count = Waybill.objects.filter(driver=driver, status__in=_DONE_STATUSES).count()
    freight = ExpenseRecord.objects.filter(
        waybill__driver=driver, direction=ExpenseRecord.DIRECTION_PAYABLE,
    ).aggregate(t=Sum("amount"))["t"] or 0
    driver.cumulative_waybills = count
    driver.cumulative_freight = freight
    driver.save(update_fields=["cumulative_waybills", "cumulative_freight", "updated_at"])


def vehicle_freight_total(vehicle) -> float:
    """车辆历史运费记录合计（该车所有运单的应付）。"""
    from apps.finance.models import ExpenseRecord

    total = ExpenseRecord.objects.filter(
        waybill__vehicle=vehicle, direction=ExpenseRecord.DIRECTION_PAYABLE,
    ).aggregate(t=Sum("amount"))["t"] or 0
    return float(total)


def customer_history(customer, *, top=3) -> dict:
    """客户历史运单数与常用线路（按订单 起→终 频次）。"""
    from .models import Order

    qs = Order.objects.filter(customer=customer).exclude(status=Order.STATUS_CANCELLED)
    routes = Counter(
        f"{o.origin or '?'}→{o.destination or '?'}"
        for o in qs.only("origin", "destination")
        if o.origin or o.destination
    )
    return {
        "order_count": qs.count(),
        "common_routes": [r for r, _ in routes.most_common(top)],
    }
