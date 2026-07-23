"""全局查单：一个输入框直接给答案（搜索即工作台）。

输入车牌 / 电话 / 运单号 / 订单号 / 客户名，解析为最相关的实体 + 实时上下文，
让命令面板的搜索结果本身成为工作台（当前运单/司机/状态/ETA + 可执行动作）。
"""

from django.utils import timezone

_ACTIVE = ("dispatched", "loaded", "departed", "in_transit", "arrived")


def _mask_phone(p: str) -> str:
    p = p or ""
    return f"{p[:3]}****{p[-4:]}" if len(p) >= 7 else p


def _waybill_card(wb) -> dict:
    from .models import Waybill

    driver_name = wb.driver.name if wb.driver_id else ""
    driver_phone = wb.driver.phone if wb.driver_id else ""
    plate = wb.vehicle.plate_no if wb.vehicle_id else ""
    eta = wb.estimated_arrival or wb.planned_arrival
    status_label = dict(Waybill.STATUS_CHOICES).get(wb.status, wb.status)
    fields = [
        {"label": "线路", "value": f"{wb.origin or '?'}→{wb.destination or '?'}"},
        {"label": "状态", "value": status_label},
    ]
    if plate:
        fields.append({"label": "车牌", "value": plate})
    if driver_name:
        fields.append({"label": "司机", "value": f"{driver_name} {_mask_phone(driver_phone)}"})
    if wb.customer_id:
        fields.append({"label": "客户", "value": wb.customer.name})
    if eta:
        fields.append({"label": "ETA", "value": timezone.localtime(eta).strftime("%m-%d %H:%M")})
    actions = ["view_waybill", "copy_reply"]
    if driver_phone:
        actions.append("call_driver")
    return {
        "kind": "waybill",
        "title": f"运单 {wb.waybill_no}",
        "waybill_no": wb.waybill_no,
        "driver_phone": driver_phone,
        "fields": fields,
        "actions": actions,
    }


def _active_waybill_for(**filt):
    from .models import Waybill

    return (
        Waybill.objects.filter(**filt).exclude(status="voided")
        .order_by("-created_at").first()
    )


def global_lookup(q: str) -> dict:
    """按 运单号 → 车牌 → 电话 → 订单号 → 客户名 顺序解析，返回首个命中的答案卡。"""
    from django.db.models import Q

    from apps.masterdata.models import Vehicle

    from .models import Order, Waybill

    q = (q or "").strip()
    if len(q) < 2:
        return {"kind": "none"}

    # 1) 运单号
    wb = Waybill.objects.filter(waybill_no__iexact=q).first() or Waybill.objects.filter(waybill_no__icontains=q).first()
    if wb:
        return _waybill_card(wb)

    # 2) 车牌 → 该车当前运单
    veh = Vehicle.objects.filter(plate_no__icontains=q).first()
    if veh:
        wb = _active_waybill_for(vehicle=veh)
        if wb:
            card = _waybill_card(wb)
            card["title"] = f"车辆 {veh.plate_no} · 当前运单"
            return card
        return {"kind": "vehicle", "title": f"车辆 {veh.plate_no}", "fields": [{"label": "状态", "value": "当前无在途运单"}], "actions": []}

    # 3) 电话 → 司机当前运单
    if q.isdigit() and len(q) >= 7:
        from apps.masterdata.models import Driver

        drv = Driver.objects.filter(phone=q).first()
        if drv:
            wb = _active_waybill_for(driver=drv)
            if wb:
                card = _waybill_card(wb)
                card["title"] = f"司机 {drv.name} · 当前运单"
                return card
            return {"kind": "driver", "title": f"司机 {drv.name}", "fields": [{"label": "电话", "value": _mask_phone(drv.phone)}, {"label": "状态", "value": "当前无在途运单"}], "actions": []}

    # 4) 订单号
    order = Order.objects.filter(order_no__iexact=q).first() or Order.objects.filter(order_no__icontains=q).first()
    if order:
        return {
            "kind": "order",
            "title": f"订单 {order.order_no}",
            "order_no": order.order_no,
            "fields": [
                {"label": "线路", "value": f"{order.origin or '?'}→{order.destination or '?'}"},
                {"label": "状态", "value": dict(Order.STATUS_CHOICES).get(order.status, order.status)},
                {"label": "客户", "value": order.customer.name if order.customer_id else "散客"},
            ],
            "actions": ["view_order"],
        }

    # 5) 客户名 → 概览
    from apps.masterdata.models import Customer

    cust = Customer.objects.filter(Q(name__icontains=q) | Q(code__iexact=q)).first()
    if cust:
        active = Waybill.objects.filter(customer=cust, status__in=_ACTIVE).count()
        return {
            "kind": "customer",
            "title": f"客户 {cust.name}",
            "customer_id": str(cust.id),
            "fields": [
                {"label": "在途运单", "value": str(active)},
                {"label": "账期", "value": f"{cust.credit_days} 天"},
            ],
            "actions": [],
        }

    return {"kind": "none"}


def global_search(q: str, *, limit: int = 12) -> dict:
    """命令面板工作台：返回「精确答案卡」+「跨实体可跳转结果列表」。

    answer：最相关单实体的实时上下文卡（沿用 global_lookup）。
    results：运单/订单/客户/承运商/对账单的多命中列表，每项可点击直达对应详情/台账。
    """
    from django.db.models import Q

    from apps.finance.models import Statement
    from apps.masterdata.models import Carrier, Customer

    from .models import Order, Waybill

    q = (q or "").strip()
    if len(q) < 2:
        return {"answer": {"kind": "none"}, "results": []}

    answer = global_lookup(q)
    results: list[dict] = []

    def status_label(model, code):
        return dict(model.STATUS_CHOICES).get(code, code) if hasattr(model, "STATUS_CHOICES") else code

    # 运单（有详情页）
    for wb in (
        Waybill.objects.select_related("customer")
        .filter(Q(waybill_no__icontains=q) | Q(customer__name__icontains=q) | Q(origin__icontains=q) | Q(destination__icontains=q))
        .exclude(status="voided").order_by("-created_at")[:5]
    ):
        results.append({
            "kind": "waybill", "title": f"运单 {wb.waybill_no}",
            "subtitle": f"{wb.origin or '?'}→{wb.destination or '?'} · {status_label(Waybill, wb.status)}"
                        + (f" · {wb.customer.name}" if wb.customer_id else ""),
            "path": f"/waybills/{wb.waybill_no}",
        })

    # 订单（有详情页）
    for od in (
        Order.objects.select_related("customer")
        .filter(Q(order_no__icontains=q) | Q(customer__name__icontains=q) | Q(origin__icontains=q) | Q(destination__icontains=q))
        .order_by("-created_at")[:5]
    ):
        results.append({
            "kind": "order", "title": f"订单 {od.order_no}",
            "subtitle": f"{od.origin or '?'}→{od.destination or '?'} · {status_label(Order, od.status)}"
                        + (f" · {od.customer.name}" if od.customer_id else ""),
            "path": f"/orders/{od.id}",
        })

    # 客户 / 承运商 / 对账单（跳对应台账）
    for c in Customer.objects.filter(Q(name__icontains=q) | Q(code__icontains=q))[:3]:
        active = Waybill.objects.filter(customer=c, status__in=_ACTIVE).count()
        results.append({
            "kind": "customer", "title": f"客户 {c.name}",
            "subtitle": f"在途 {active} · 账期 {c.credit_days} 天", "path": "/fleet",
        })
    for c in Carrier.objects.filter(Q(name__icontains=q) | Q(code__icontains=q))[:3]:
        results.append({
            "kind": "carrier", "title": f"承运商 {c.name}",
            "subtitle": f"{c.city or '—'} · {c.grade or '—'}级", "path": "/fleet",
        })
    for s in Statement.objects.filter(Q(statement_no__icontains=q) | Q(counterparty_name__icontains=q))[:3]:
        results.append({
            "kind": "statement", "title": f"对账单 {s.statement_no}",
            "subtitle": f"{s.counterparty_name} · {status_label(Statement, s.status)}", "path": "/reconciliation",
        })

    return {"answer": answer, "results": results[:limit]}
