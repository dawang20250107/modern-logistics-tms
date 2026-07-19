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
