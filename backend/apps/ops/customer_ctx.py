"""客服工作台：客户上下文 + 建单补全 + 客户回复卡。

B2B 客户重复线路多、固定客户多、地址联系人重复、月结对账严格、催单频繁。
本模块把这些沉淀成客服可用的效率数据：选中客户即带出上下文，建单自动补全，
查单一张"客户回复卡"10 秒可复制回复。
"""

from collections import Counter

from django.db.models import Sum
from django.utils import timezone


def _addr_rows(orders, addr_field, name_field, phone_field, top=5):
    counter = Counter()
    meta = {}
    for o in orders:
        addr = getattr(o, addr_field, "") or ""
        if not addr:
            continue
        counter[addr] += 1
        meta[addr] = {
            "address": addr,
            "contact_name": getattr(o, name_field, "") or "",
            "contact_phone": getattr(o, phone_field, "") or "",
        }
    return [{**meta[a], "count": n} for a, n in counter.most_common(top)]


def _order_brief(o) -> dict:
    from .models import Order

    return {
        "order_no": o.order_no,
        "status": o.status,
        "status_label": dict(Order.STATUS_CHOICES).get(o.status, o.status),
        "route": f"{o.origin or '?'}→{o.destination or '?'}",
        "cargo": o.cargo_desc or "",
        "quoted_amount": float(o.quoted_amount or 0),
        "created_at": o.created_at.isoformat() if o.created_at else None,
    }


def customer_context(customer) -> dict:
    """选中客户后带出的全部上下文：等级/账期/授信、常用线路/地址、最近/未完成/异常/回单未返订单。"""
    from apps.finance.models import ExpenseRecord

    from .models import Order, Waybill

    orders = list(
        Order.objects.filter(customer=customer)
        .exclude(status=Order.STATUS_CANCELLED)
        .order_by("-created_at")
    )
    routes = Counter(
        f"{o.origin or '?'}→{o.destination or '?'}" for o in orders if o.origin or o.destination
    )

    # 授信占用：该客户未结算运单的应收合计
    outstanding = (
        ExpenseRecord.objects.filter(direction="receivable", waybill__customer=customer)
        .exclude(waybill__status=Waybill.STATUS_SETTLED)
        .aggregate(s=Sum("amount"))
        .get("s")
        or 0
    )
    credit_limit = float(customer.credit_limit or 0)

    open_statuses = {
        Order.STATUS_PENDING_CONFIRM, Order.STATUS_CONFIRMED,
        Order.STATUS_POOLED, Order.STATUS_DISPATCHING,
    }
    open_orders = [o for o in orders if o.status in open_statuses]

    # 异常 / 回单未返（运单侧）
    exc_count = (
        Waybill.objects.filter(customer=customer, exceptions__isnull=False)
        .exclude(exceptions__status="resolved").distinct().count()
    )
    receipt_pending = Waybill.objects.filter(
        customer=customer, status__in=[Waybill.STATUS_SIGNED, Waybill.STATUS_DELIVERED],
    ).exclude(receipt_status__in=["returned", "audited"]).count()

    return {
        "customer_id": str(customer.id),
        "name": customer.name,
        "profile": {
            "settlement_type": customer.settlement_type or "",
            "credit_limit": credit_limit,
            "credit_days": customer.credit_days,
            "billing_day": customer.billing_day,
        },
        "credit": {
            "limit": credit_limit,
            "outstanding": float(outstanding),
            "available": (credit_limit - float(outstanding)) if credit_limit else None,
            "used_pct": round(float(outstanding) / credit_limit, 3) if credit_limit else None,
            "over_limit": bool(credit_limit and float(outstanding) > credit_limit),
        },
        "common_routes": [r for r, _ in routes.most_common(5)],
        "common_pickups": _addr_rows(orders, "pickup_address", "pickup_contact_name", "pickup_contact_phone"),
        "common_deliveries": _addr_rows(orders, "delivery_address", "delivery_contact_name", "delivery_contact_phone"),
        "recent_orders": [_order_brief(o) for o in orders[:5]],
        "open_orders": [_order_brief(o) for o in open_orders[:8]],
        "counts": {
            "total": len(orders),
            "open": len(open_orders),
            "exceptions": exc_count,
            "receipt_pending": receipt_pending,
        },
    }


def lane_suggest(customer, origin: str, destination: str) -> dict:
    """建单补全：某客户在该线路的常见货物 + 参考价区间 + 历史收货方。"""
    from apps.masterdata.models import CarrierLanePrice

    from .models import Order

    qs = Order.objects.filter(customer=customer).exclude(status=Order.STATUS_CANCELLED)
    if origin:
        qs = qs.filter(origin=origin)
    if destination:
        qs = qs.filter(destination=destination)
    orders = list(qs.order_by("-created_at")[:100])

    cargo = Counter(o.cargo_desc for o in orders if o.cargo_desc)
    quotes = [float(o.quoted_amount) for o in orders if o.quoted_amount]

    # 线路价库参考价区间（承运侧成本，供客服报价参考）
    lane_qs = CarrierLanePrice.objects.filter(is_active=True)
    if origin and destination:
        lane_qs = lane_qs.filter(origin_city=origin, dest_city=destination)
    lane_prices = [float(x.standard_price) for x in lane_qs if x.standard_price]

    price_band = None
    if quotes:
        price_band = [round(min(quotes)), round(max(quotes))]
    elif lane_prices:
        price_band = [round(min(lane_prices)), round(max(lane_prices))]

    return {
        "common_cargo": [c for c, _ in cargo.most_common(5)],
        "price_band": price_band,
        "cost_reference": [round(min(lane_prices)), round(max(lane_prices))] if lane_prices else None,
        "common_deliveries": _addr_rows(orders, "delivery_address", "delivery_contact_name", "delivery_contact_phone"),
    }


def _latest_node(waybill) -> dict | None:
    """最近节点：优先取已实际到发的停靠点，否则取最近运单事件。"""
    stop = (
        waybill.stops.exclude(actual_arrival_at__isnull=True)
        .order_by("-actual_arrival_at").first()
        if hasattr(waybill, "stops") else None
    )
    if stop:
        return {
            "node": f"{stop.get_stop_type_display()} · {stop.city or ''}".strip(" ·"),
            "at": stop.actual_arrival_at.isoformat() if stop.actual_arrival_at else None,
        }
    evt = waybill.events.order_by("-event_time").first() if hasattr(waybill, "events") else None
    if evt:
        return {"node": evt.event_type, "at": evt.event_time.isoformat() if evt.event_time else None}
    return None


def reply_card(waybill) -> dict:
    """客户回复卡：当前状态/司机/车牌/最近节点/预计到达/异常/回单 + 一段可复制文案。"""
    from .models import ExceptionRecord, Waybill

    status_label = dict(Waybill.STATUS_CHOICES).get(waybill.status, waybill.status)
    node = _latest_node(waybill)
    eta = waybill.estimated_arrival or waybill.planned_arrival
    exc = (
        ExceptionRecord.objects.filter(waybill=waybill)
        .exclude(status="resolved").order_by("-created_at").first()
    )
    receipt_map = {"returned": "已回收", "audited": "已核销"}
    receipt = receipt_map.get(waybill.receipt_status, "待回收")

    driver_name = getattr(waybill.driver, "name", "") if waybill.driver_id else ""
    driver_phone = getattr(waybill.driver, "phone", "") if waybill.driver_id else ""
    plate = getattr(waybill.vehicle, "plate_no", "") if waybill.vehicle_id else ""

    lines = [
        f"【{waybill.waybill_no}】{waybill.origin or '?'}→{waybill.destination or '?'}",
        f"当前状态：{status_label}",
    ]
    if plate or driver_name:
        lines.append(f"承运：{plate} {driver_name} {driver_phone}".strip())
    if node:
        lines.append(f"最近节点：{node['node']}")
    if eta:
        lines.append(f"预计到达：{timezone.localtime(eta):%m-%d %H:%M}")
    lines.append(f"回单：{receipt}")
    if exc:
        lines.append(f"异常：{dict(ExceptionRecord.EXCEPTION_TYPE_CHOICES).get(exc.exception_type, exc.exception_type)}")

    return {
        "waybill_no": waybill.waybill_no,
        "route": f"{waybill.origin or '?'}→{waybill.destination or '?'}",
        "status": waybill.status,
        "status_label": status_label,
        "driver_name": driver_name,
        "driver_phone": driver_phone,
        "plate_no": plate,
        "latest_node": node,
        "eta": eta.isoformat() if eta else None,
        "receipt_status": receipt,
        "exception": (dict(ExceptionRecord.EXCEPTION_TYPE_CHOICES).get(exc.exception_type, exc.exception_type) if exc else None),
        "copy_text": "\n".join(lines),
    }
