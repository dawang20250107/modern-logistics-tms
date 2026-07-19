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


def is_chief_dispatcher(user) -> bool:
    """总调度：可分单、可调派任意池中订单。以 dispatch.assign 权限点或超管判定。"""
    if not user or not getattr(user, "is_authenticated", False):
        return False
    if user.is_superuser:
        return True
    from apps.iam.services import effective_permissions

    perms = effective_permissions(user)
    return "*" in perms or "dispatch.assign" in perms


def claim_order(order_id, dispatcher) -> Order:
    """调度锁定订单（行锁防并发抢单）。分派给他人的订单，非总调度不可锁定。"""
    with transaction.atomic():
        order = Order.objects.select_for_update().filter(id=order_id).first()
        if order is None:
            raise AppError("ORDER_NOT_FOUND", "订单不存在。", status=404)
        if order.status != Order.STATUS_POOLED or order.claimed_by_id:
            raise AppError("ORDER_NOT_CLAIMABLE", "订单已被锁定或不在池中。", status=409)
        did = getattr(dispatcher, "id", None)
        if order.assigned_to_id and order.assigned_to_id != did and not is_chief_dispatcher(dispatcher):
            raise AppError("ORDER_ASSIGNED_OTHER", "该订单已由总调度分派给其他调度，不可锁定。", status=409)
        order.claimed_by = dispatcher if dispatcher and dispatcher.is_authenticated else None
        order.claimed_at = timezone.now()
        order.status = Order.STATUS_DISPATCHING
        order.save(update_fields=["claimed_by", "claimed_at", "status", "updated_at"])
        record_order_event(order, "claimed", actor=dispatcher, to_status=order.status, source="dispatch")
    publish_event("order_claimed", {"order_no": order.order_no, "dispatcher": getattr(dispatcher, "username", "")})
    return order


def assign_orders(order_ids, dispatcher_id, operator) -> dict:
    """总调度分单：把池中订单指派给某个调度（行锁；跳过已被他人锁定的单）。"""
    if not is_chief_dispatcher(operator):
        raise AppError("NOT_CHIEF", "仅总调度可分单。", status=403)
    from django.contrib.auth import get_user_model

    target = get_user_model().objects.filter(id=dispatcher_id).first()
    if target is None:
        raise AppError("DISPATCHER_NOT_FOUND", "目标调度不存在。", status=404)
    assigned, skipped = [], []
    with transaction.atomic():
        orders = list(Order.objects.select_for_update().filter(id__in=list(order_ids or [])))
        for order in orders:
            if order.status not in (Order.STATUS_POOLED, Order.STATUS_DISPATCHING):
                skipped.append(order.order_no)
                continue
            if order.claimed_by_id and order.claimed_by_id != target.id:
                skipped.append(order.order_no)  # 已被他人锁定，分单跳过
                continue
            order.assigned_to = target
            order.assigned_by = operator
            order.assigned_at = timezone.now()
            order.save(update_fields=["assigned_to", "assigned_by", "assigned_at", "updated_at"])
            record_order_event(
                order, "assigned", actor=operator, source="dispatch",
                note=f"分派给 {target.nickname or target.username}",
            )
            assigned.append(order.order_no)
    for no in assigned:
        publish_event("order_assigned", {"order_no": no, "dispatcher": target.username})
    return {"assigned": assigned, "skipped": skipped, "dispatcher": target.nickname or target.username}


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


def unassign_order(order, operator) -> Order:
    """总调度撤销分单。"""
    if not is_chief_dispatcher(operator):
        raise AppError("NOT_CHIEF", "仅总调度可撤销分单。", status=403)
    order.assigned_to = None
    order.assigned_by = None
    order.assigned_at = None
    order.save(update_fields=["assigned_to", "assigned_by", "assigned_at", "updated_at"])
    publish_event("order_pooled", {"order_no": order.order_no, "unassigned": True})
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


def _assert_dispatch_requirements(dispatch_type, carrier, vehicle, platform_name):
    """按派单类型校验必填要素：外包必须承运商，网货必须平台名，自营必须车辆。"""
    if dispatch_type == Waybill.DISPATCH_THIRD_PARTY and carrier is None:
        raise AppError("CARRIER_REQUIRED", "外包承运商派单必须选择承运商。", status=400)
    if dispatch_type == Waybill.DISPATCH_PLATFORM and not (platform_name or "").strip():
        raise AppError("PLATFORM_REQUIRED", "网货平台派单必须填写平台名称。", status=400)
    if dispatch_type in (Waybill.DISPATCH_OWN, Waybill.DISPATCH_FLEET) and vehicle is None:
        raise AppError("VEHICLE_REQUIRED", "自营派单必须选择车辆。", status=400)


def _dispatch_status_for(dispatch_type, driver) -> str:
    """初始派单后的承运状态。

    - 外包：承运商待接单（pending_accept）；若已回填司机则进入待执行（pending_driver_submit 之后）。
    - 网货：平台侧承接（pending_accept）。
    - 自营：有司机→已指派待执行；无司机→待回填司机车辆（pending_driver_submit）。
    """
    if dispatch_type == Waybill.DISPATCH_THIRD_PARTY:
        return "accepted" if driver else "pending_accept"
    if dispatch_type == Waybill.DISPATCH_PLATFORM:
        return "pending_accept"
    return "driver_assigned" if driver else "pending_driver_submit"


def _snapshot_payable(waybill, order, *, dispatch_type, carrier, driver, platform_name,
                      agreed_payable_amount, price_source, quote_id, price_remark):
    """派单议定应付金额快照：生成应付 ExpenseRecord，记录价格来源。

    对账以此快照为准，不再重新计算，避免规则/价库后续变动导致历史对账不可解释。
    """
    if agreed_payable_amount is None:
        return None
    from decimal import Decimal

    from apps.finance.models import ExpenseRecord

    amount = Decimal(str(agreed_payable_amount))
    if amount <= 0:
        return None
    if dispatch_type == Waybill.DISPATCH_PLATFORM:
        payee_type, payee_ref = "platform", (platform_name or "网货平台")
    elif carrier is not None:
        payee_type, payee_ref = "carrier", carrier.name
    elif driver is not None:
        payee_type, payee_ref = "driver", driver.name
    else:
        payee_type, payee_ref = "other", ""
    return ExpenseRecord.objects.create(
        waybill=waybill, direction="payable", expense_item_code="freight", amount=amount,
        occurred_at=timezone.now(), payee_type=payee_type, payee_ref=payee_ref,
        price_source=price_source or "manual", quote_id=quote_id or "", remark=price_remark or "",
        input_snapshot={
            "weight_ton": float(order.cargo_weight_ton or 0),
            "volume_cbm": float(order.cargo_volume_cbm or 0),
            "quantity": int(order.cargo_quantity or 0),
            "route": f"{order.origin or ''}→{order.destination or ''}",
        },
        calculation_detail={"agreed_payable": float(amount), "note": "派单议定应付金额快照"},
    )


def dispatch_order(order, *, dispatch_type, carrier=None, vehicle=None, driver=None,
                   trailer=None, co_drivers=None, platform_name="", platform_order_no="",
                   agreed_payable_amount=None, price_source="", quote_id="", price_remark="",
                   operator=None):
    """派单：按派单类型校验要素 → 生成运单 → 落承运信息与承运状态 → 议定应付金额快照 → 事件。

    形成闭环：推荐（综合推荐承运商）→ 派单（校验+落库）→ 费用（应付快照）→ 承运商接单（承运状态流转）。
    并发安全：锁定车辆/司机行后校验占用，避免两名调度把同一车/司机重复派出。
    """
    if dispatch_type not in dict(Waybill.DISPATCH_TYPE_CHOICES):
        raise AppError("INVALID_DISPATCH_TYPE", "派单类型非法。", status=400)
    if order.status not in (Order.STATUS_POOLED, Order.STATUS_DISPATCHING, Order.STATUS_CONFIRMED):
        raise AppError("ORDER_NOT_DISPATCHABLE", "订单当前状态不可派单。", status=409)
    # 权限门禁：非总调度只能调派"分派给自己"或"自己锁定"的订单
    if operator is not None and not is_chief_dispatcher(operator):
        owner_ids = {order.claimed_by_id, order.assigned_to_id}
        if getattr(operator, "id", None) not in owner_ids:
            raise AppError(
                "ORDER_NOT_YOURS",
                "该订单未分派/锁定给你：请由总调度分单，或先自行锁定后再调派。",
                status=403,
            )
    _assert_dispatch_requirements(dispatch_type, carrier, vehicle, platform_name)

    with transaction.atomic():
        from apps.masterdata.models import Driver, Vehicle

        from .models import WaybillEvent

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
        # 承运状态：按通道与是否已回填司机流转
        waybill.dispatch_status = _dispatch_status_for(dispatch_type, driver)
        waybill.save(update_fields=["dispatch_status", "updated_at"])
        # 费用：议定应付金额快照（对账以此为准）
        expense = _snapshot_payable(
            waybill, order, dispatch_type=dispatch_type, carrier=carrier, driver=driver,
            platform_name=platform_name, agreed_payable_amount=agreed_payable_amount,
            price_source=price_source, quote_id=quote_id, price_remark=price_remark,
        )
        # 事件：订单事件 + 运单事件（含价格来源，供对账与追溯）
        record_order_event(
            order, "dispatched", actor=operator, to_status=order.status, source="dispatch",
            waybill_no=waybill.waybill_no, dispatch_type=dispatch_type,
        )
        WaybillEvent.objects.create(
            waybill=waybill, event_type="dispatched", event_time=timezone.now(), source="dispatch",
            resource=(carrier.name if carrier else platform_name or (waybill.vehicle.plate_no if waybill.vehicle_id else "")),
            payload={
                "dispatch_type": dispatch_type,
                "dispatch_status": waybill.dispatch_status,
                "price_source": price_source or ("" if expense is None else "manual"),
                "agreed_payable": float(agreed_payable_amount) if agreed_payable_amount else 0,
                "quote_id": quote_id or "",
            },
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


class _LaneNeed:
    """代表一组同向合并后的承运需求（供承运商评分用的轻量对象）。"""

    def __init__(self, origin, destination, weight_ton, volume_cbm):
        self.origin = origin
        self.destination = destination
        self.cargo_weight_ton = weight_ton
        self.cargo_volume_cbm = volume_cbm


def plan_dispatch_orders(orders) -> dict:
    """批量智能排线（承运商化）：同向 LTL 订单合成整车承运需求 → 推荐承运商 + 整车报价，
    生成询价/派单建议。自营车配载仅作兜底参考，不再作为主推荐。

    仅作为算法推荐方案返回，不自动落库，供调度员人工审阅确认。
    """
    from .carrier_scoring import carrier_recommendation, score_carriers
    from .dispatch import consolidate_and_group_orders

    plan = consolidate_and_group_orders(orders)
    # 为每条合并线路推荐合适承运商（找合适承运商，不是找车）
    for trip in plan.get("consolidated_trips", []):
        need = _LaneNeed(trip.get("origin", ""), trip.get("destination", ""),
                         trip.get("total_weight_ton", 0), trip.get("total_volume_cbm", 0))
        trip["carrier_recommendation"] = carrier_recommendation(need)
        trip["carrier_candidates"] = score_carriers(need, top=3)
    return plan

