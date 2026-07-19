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
    """校验车辆核载/容积 + 车厢结构满足订单要求（运力未知则放行），避免误派超载/敞车拉冷链危货。"""
    from .dispatch import body_type_mismatch, vehicle_fit, waybill_requirements

    if not vehicle:
        return
    reqs = waybill_requirements(order)
    body_issue = body_type_mismatch(vehicle, reqs)
    if body_issue:
        raise AppError("VEHICLE_BODY_MISMATCH", f"车辆 {vehicle.plate_no} 车厢结构不符：{body_issue}。", status=409)
    if vehicle_fit(vehicle, order, reqs=reqs) is None:
        raise AppError(
            "VEHICLE_OVERLOADED",
            f"车辆 {vehicle.plate_no} 核载/容积不足以承运该订单货量，请改派更大车型。",
            status=409,
        )


def _assert_compliance(vehicle, driver, order):
    """派单硬合规：证件过期车辆（可配置）与准驾不符/资质缺失的司机一律拦截，不上违规车。"""
    from django.conf import settings

    from .dispatch import driver_qualification_issues, vehicle_compliance_issues, waybill_requirements

    if vehicle and getattr(settings, "DISPATCH_BLOCK_ON_EXPIRED", True):
        issues = vehicle_compliance_issues(vehicle)
        if issues:
            raise AppError(
                "VEHICLE_NON_COMPLIANT",
                f"车辆 {vehicle.plate_no} 证件过期（{'/'.join(issues)}），不可派车。",
                status=409,
            )
    if driver:
        is_hazmat = waybill_requirements(order).get("is_hazmat", False)
        d_issues = driver_qualification_issues(driver, vehicle, is_hazmat=is_hazmat)
        if d_issues:
            raise AppError(
                "DRIVER_NON_QUALIFIED",
                f"司机 {driver.name} 资质不符（{'/'.join(d_issues)}），不可派单。",
                status=409,
            )


def _assert_carrier_allowed(carrier):
    """承运商风控硬阻断：黑名单/停用一律拦截，承运资质过期按开关拦截。自有车派单 carrier 为空则放行。"""
    if carrier is None:
        return
    from django.conf import settings

    reason = carrier.dispatch_block_reason(
        block_on_expired=getattr(settings, "DISPATCH_BLOCK_ON_EXPIRED", True)
    )
    if reason:
        raise AppError("CARRIER_NOT_ALLOWED", f"{reason}，不可派单。", status=409)


def dispatch_order(order, *, dispatch_type, carrier=None, vehicle=None, driver=None,
                   trailer=None, co_drivers=None, platform_name="", platform_order_no="", operator=None):
    """派单：生成运单并落承运信息（牵引车/挂车/主副驾）与派单类型，回写订单为已派单。

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
        _assert_carrier_allowed(carrier)
        _assert_resource_free(vehicle, driver)
        _assert_capacity_fit(vehicle, order)
        _assert_compliance(vehicle, driver, order)
        waybill = convert_order_to_waybill(
            order, carrier=carrier, vehicle=vehicle, driver=driver, trailer=trailer,
            co_drivers=co_drivers, dispatch_type=dispatch_type,
            platform_name=platform_name, platform_order_no=platform_order_no, operator=operator,
        )
        record_order_event(
            order, "dispatched", actor=operator, to_status=order.status, source="dispatch",
            waybill_no=waybill.waybill_no, dispatch_type=dispatch_type,
        )
        # 工作流编排：派单即自动生成承运合同（告别文字版合同）
        if driver is not None:
            from .contracts import generate_contract

            generate_contract(waybill, operator=operator)
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
    """AI 派单建议（承运商优先）。

    产品原则：调度不是"找车"，而是"找合适承运商"。默认推荐**外包承运商**，
    辅助**网货平台**，自营车仅特殊场景兜底。承运商按线路履约表现评分排序，
    给出可执行建议 + 建议价区间 + 风险说明 + 是否需主管确认（建议态，不自动派）。
    """
    from apps.integrations.ymm import freight_quote

    from .carrier_scoring import carrier_recommendation, score_carriers
    from .dispatch import carrier_quotes, rank_vehicles

    carrier_rows = score_carriers(order)
    recommendation = carrier_recommendation(order)
    vehicles = rank_vehicles(order)[:3]
    quotes = carrier_quotes(order)
    signals = external_signals(order)
    # 运满满调车运费比价（外部参考价；未接入则离线参考）
    ymm = freight_quote(
        order.origin, order.destination,
        weight_ton=order.cargo_weight_ton, volume_cbm=order.cargo_volume_cbm,
    )
    # 派单类型建议：外包承运商优先 → 网货平台辅助 → 自营车兜底
    if carrier_rows:
        suggested_type = "third_party"
    elif ymm and ymm.get("avg"):
        suggested_type = "platform"
    elif vehicles:
        suggested_type = "own_vehicle"
    else:
        suggested_type = "third_party"
    return {
        "order_no": order.order_no,
        "cargo": {"weight_ton": float(order.cargo_weight_ton), "volume_cbm": float(order.cargo_volume_cbm)},
        "carrier_recommendations": carrier_rows,
        "recommendation": recommendation,
        "vehicle_candidates": vehicles,
        "carrier_quotes": quotes,
        "ymm_quote": ymm,
        "external_signals": signals,
        "suggested_dispatch_type": suggested_type,
        "best_vehicle": vehicles[0] if vehicles else None,
        "best_carrier": quotes[0] if quotes else None,
    }


def plan_dispatch_orders(orders) -> dict:
    """批量智能排线：将相同方向的多个小 LTL 订单合并为一个 FTL 拼单大车派送，并计算降本测算。

    仅作为 AI 算法推荐方案返回，不自动落库，供调度员人工审阅确认。
    """
    from .dispatch import consolidate_and_group_orders

    return consolidate_and_group_orders(orders)

