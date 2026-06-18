"""订单池调度：并发安全认领、派单（自有单车/车队/三方），及 AI 派单建议。

多调度并发：认领用 select_for_update 行锁 + 状态校验，保证一单只被一名调度抢到。
AI 建议：基于系统数据池（可用运力/承运商比价）+ 订单属性外部信号，给调度参考。
"""

from django.db import transaction
from django.utils import timezone

from apps.core.exceptions import AppError
from apps.core.redis import publish_event

from .intake import convert_order_to_waybill, record_order_event
from .models import Order, Waybill

# 占用运力的运单状态（视为不可再次派给同一车辆/司机）
_BUSY_STATUSES = [
    Waybill.STATUS_DISPATCHED, Waybill.STATUS_LOADED, Waybill.STATUS_DEPARTED,
    Waybill.STATUS_IN_TRANSIT, Waybill.STATUS_PENDING_DISPATCH,
]


def claim_order(order_id, dispatcher) -> Order:
    """调度认领订单（行锁防并发抢单）。"""
    with transaction.atomic():
        order = Order.objects.select_for_update().filter(id=order_id).first()
        if order is None:
            raise AppError("ORDER_NOT_FOUND", "订单不存在。", status=404)
        if order.status != Order.STATUS_POOLED or order.claimed_by_id:
            raise AppError("ORDER_NOT_CLAIMABLE", "订单已被认领或不在池中。", status=409)
        order.claimed_by = dispatcher if dispatcher and dispatcher.is_authenticated else None
        order.claimed_at = timezone.now()
        order.status = Order.STATUS_DISPATCHING
        order.save(update_fields=["claimed_by", "claimed_at", "status", "updated_at"])
        record_order_event(order, "claimed", actor=dispatcher, to_status=order.status, source="dispatch")
    publish_event("order_claimed", {"order_no": order.order_no, "dispatcher": getattr(dispatcher, "username", "")})
    return order


def release_order(order, dispatcher=None) -> Order:
    """退回订单池（撤销认领）。"""
    if order.status != Order.STATUS_DISPATCHING:
        raise AppError("ORDER_NOT_DISPATCHING", "仅调度中订单可退回池。", status=409)
    order.status = Order.STATUS_POOLED
    order.claimed_by = None
    order.claimed_at = None
    order.save(update_fields=["status", "claimed_by", "claimed_at", "updated_at"])
    publish_event("order_pooled", {"order_no": order.order_no, "released": True})
    return order


def _assert_resource_free(vehicle, driver):
    """校验车辆/司机未被占用（已在调用方持有行锁，保证并发不重复派）。"""
    if vehicle and Waybill.objects.filter(vehicle=vehicle, status__in=_BUSY_STATUSES).exists():
        raise AppError("VEHICLE_BUSY", f"车辆 {vehicle.plate_no} 已被占用，不可重复派单。", status=409)
    if driver and Waybill.objects.filter(driver=driver, status__in=_BUSY_STATUSES).exists():
        raise AppError("DRIVER_BUSY", f"司机 {driver.name} 已被占用，不可重复派单。", status=409)


def _assert_capacity_fit(vehicle, order):
    """校验车辆核载/容积满足订单货量（运力未知则放行），避免误派超载车。"""
    from .dispatch import vehicle_fit

    if vehicle and vehicle_fit(vehicle, order) is None:
        raise AppError(
            "VEHICLE_OVERLOADED",
            f"车辆 {vehicle.plate_no} 核载/容积不足以承运该订单货量，请改派更大车型。",
            status=409,
        )


def dispatch_order(order, *, dispatch_type, carrier=None, vehicle=None, driver=None, operator=None):
    """派单：生成运单并落承运信息与派单类型，回写订单为已派单。

    并发安全：锁定车辆/司机行后校验占用，避免两名调度把同一车/司机重复派出。
    """
    if dispatch_type not in dict(Waybill.DISPATCH_TYPE_CHOICES):
        raise AppError("INVALID_DISPATCH_TYPE", "派单类型非法。", status=400)
    if order.status not in (Order.STATUS_POOLED, Order.STATUS_DISPATCHING, Order.STATUS_CONFIRMED):
        raise AppError("ORDER_NOT_DISPATCHABLE", "订单当前状态不可派单。", status=409)

    with transaction.atomic():
        from apps.masterdata.models import Driver, Vehicle

        if vehicle:
            vehicle = Vehicle.objects.select_for_update().get(id=vehicle.id)
        if driver:
            driver = Driver.objects.select_for_update().get(id=driver.id)
        _assert_resource_free(vehicle, driver)
        _assert_capacity_fit(vehicle, order)
        waybill = convert_order_to_waybill(
            order, carrier=carrier, vehicle=vehicle, driver=driver, dispatch_type=dispatch_type, operator=operator
        )
        record_order_event(
            order, "dispatched", actor=operator, to_status=order.status, source="dispatch",
            waybill_no=waybill.waybill_no, dispatch_type=dispatch_type,
        )
    publish_event("order_dispatched", {
        "order_no": order.order_no, "waybill_no": waybill.waybill_no, "dispatch_type": dispatch_type,
    })
    return waybill


def external_signals(order) -> list[dict]:
    """外部/规则信号（为对接外部接口预留：天气/路况/征信等）。当前基于订单属性给出规则信号。"""
    signals = []
    if order.is_hazardous:
        signals.append({"type": "hazardous", "level": "high", "note": "危险品，需具备危运资质的承运商/车辆"})
    if order.business_type == Order.BIZ_COLDCHAIN or order.temperature_range:
        signals.append({"type": "coldchain", "level": "high", "note": f"冷链温区 {order.temperature_range or '未填'}，需温控车"})
    if order.priority in ("urgent", "vip"):
        signals.append({"type": "priority", "level": "medium", "note": f"{order.priority} 优先级，建议优先调度并预留时效"})
    return signals


def recommend_dispatch_for_order(order) -> dict:
    """AI 派单建议：系统数据池(运力/比价) + 外部信号，给调度参考（建议态，不自动派）。"""
    from .dispatch import carrier_quotes, rank_vehicles

    vehicles = rank_vehicles(order)[:3]
    quotes = carrier_quotes(order)
    signals = external_signals(order)
    # 派单类型建议：有合适自有车→自有；否则三方
    suggested_type = "own_vehicle" if vehicles else "third_party"
    return {
        "order_no": order.order_no,
        "cargo": {"weight_ton": float(order.cargo_weight_ton), "volume_cbm": float(order.cargo_volume_cbm)},
        "vehicle_candidates": vehicles,
        "carrier_quotes": quotes,
        "external_signals": signals,
        "suggested_dispatch_type": suggested_type,
        "best_vehicle": vehicles[0] if vehicles else None,
        "best_carrier": quotes[0] if quotes else None,
    }
