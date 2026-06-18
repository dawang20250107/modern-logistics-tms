"""多渠道建单：自然语言/微信群消息 → 结构化订单，及订单转运单。

parse_order_text：DeepSeek 可用时用 LLM 抽取，否则规则兜底（离线可测、可用）。
create_order_from_intake：统一入口落 Order（待确认）。
convert_order_to_waybill：人工确认后转运单（高风险写，经确认闭环）。
"""

import json
import re

from django.utils import timezone

from apps.core.exceptions import AppError

from .models import Order, OrderEvent, Waybill
from .numbering import order_no as gen_order_no
from .numbering import waybill_no as gen_waybill_no

# 单次批量操作上限，避免误传超大列表拖垮请求（交付级输入边界防护）
MAX_BATCH_SIZE = 500


def recompute_cargo_totals(order) -> None:
    """有货物明细行时，按明细汇总回写订单货量/件数/体积，保证总量一致。"""
    from django.db.models import Sum

    agg = order.cargo_items.aggregate(
        q=Sum("quantity"), w=Sum("weight_ton"), v=Sum("volume_cbm")
    )
    if order.cargo_items.exists():
        order.cargo_quantity = agg["q"] or 0
        order.cargo_weight_ton = agg["w"] or 0
        order.cargo_volume_cbm = agg["v"] or 0
        order.save(update_fields=["cargo_quantity", "cargo_weight_ton", "cargo_volume_cbm", "updated_at"])


def record_order_event(order, event_type, *, actor=None, from_status="", to_status="", source="system", **payload):
    """记录订单事件（溯源）。actor 仅在已认证时落库。"""
    OrderEvent.objects.create(
        order=order,
        event_type=event_type,
        from_status=from_status,
        to_status=to_status,
        actor=actor if actor and getattr(actor, "is_authenticated", False) else None,
        source=source,
        payload=payload,
    )

_PHONE = re.compile(r"1[3-9]\d{9}")
_WEIGHT = re.compile(r"(\d+(?:\.\d+)?)\s*(?:吨|t|T)")
_VOLUME = re.compile(r"(\d+(?:\.\d+)?)\s*(?:方|立方|m3|cbm|CBM)")
_QUANTITY = re.compile(r"(\d+)\s*(?:件|箱|托|板|pcs|PCS)")
_ROUTE = re.compile(r"([一-龥A-Za-z]{2,10})\s*(?:到|至|发往|发|->|→|—|-)\s*([一-龥A-Za-z]{2,10})")


def parse_order_text_rule(text: str) -> dict:
    """规则解析（兜底，无需外呼）。"""
    fields: dict = {}
    if m := _PHONE.search(text):
        fields["contact_phone"] = m.group(0)
    if m := _WEIGHT.search(text):
        fields["cargo_weight_ton"] = float(m.group(1))
    if m := _VOLUME.search(text):
        fields["cargo_volume_cbm"] = float(m.group(1))
    if m := _QUANTITY.search(text):
        fields["cargo_quantity"] = int(m.group(1))
    if m := _ROUTE.search(text):
        fields["origin"] = m.group(1)
        fields["destination"] = m.group(2)
    fields["cargo_desc"] = text.strip()[:255]
    return fields


def parse_order_text(text: str) -> dict:
    """优先 DeepSeek 结构化抽取，失败回退规则；返回 (fields, meta) 合并字典。"""
    from apps.ai.services.deepseek import DeepSeekClient, DeepSeekError

    client = DeepSeekClient()
    if client.is_configured:
        prompt = (
            "从下面的物流下单消息中抽取 JSON，字段：origin(始发地),destination(目的地),"
            "contact_phone(电话),cargo_desc(货物),cargo_quantity(件数,整数),"
            "cargo_weight_ton(吨,数字),cargo_volume_cbm(方,数字)。只输出 JSON。\n消息：" + text
        )
        try:
            resp = client.chat_completion(
                messages=[
                    {"role": "system", "content": "你是物流客服建单助手，只输出严格 JSON。"},
                    {"role": "user", "content": prompt},
                ]
            )
            content = resp.get("choices", [{}])[0].get("message", {}).get("content", "")
            fields = json.loads(_extract_json(content))
            fields["_meta"] = {"source": "deepseek"}
            return fields
        except (DeepSeekError, ValueError, json.JSONDecodeError, KeyError):
            pass
    fields = parse_order_text_rule(text)
    fields["_meta"] = {"source": "rule"}
    return fields


def _extract_json(content: str) -> str:
    start, end = content.find("{"), content.rfind("}")
    if start == -1 or end == -1:
        raise ValueError("no json")
    return content[start : end + 1]


def _coerce_datetimes(data: dict):
    """把 ISO 字符串的时间字段解析为 aware datetime，保证内存对象与库一致。"""
    from django.utils import timezone as tz
    from django.utils.dateparse import parse_datetime

    for field in ("expected_pickup_at", "expected_delivery_at"):
        val = data.get(field)
        if isinstance(val, str):
            dt = parse_datetime(val)
            if dt is not None:
                data[field] = tz.make_aware(dt) if tz.is_naive(dt) else dt
            else:
                data.pop(field, None)


# 可由解析/手填写入订单的业务字段白名单
_ORDER_FIELDS = (
    "contact_name", "contact_phone", "origin", "destination",
    "pickup_address", "pickup_contact_name", "pickup_contact_phone",
    "delivery_address", "delivery_contact_name", "delivery_contact_phone",
    "cargo_desc", "cargo_quantity", "cargo_weight_ton", "cargo_volume_cbm",
    "cargo_value", "package_type", "is_hazardous", "temperature_range",
    "source_type", "business_type", "priority", "settlement_type", "quoted_amount",
    "expected_pickup_at", "expected_delivery_at", "remark",
)


def create_order_from_intake(*, text: str = "", fields: dict | None = None, channel: str = Order.CHANNEL_CS,
                             source: str = "", customer=None, operator=None) -> Order:
    """统一建单入口：先 AI/规则解析 text，再用显式 fields 覆盖（人工改优先），落待确认订单。"""
    data: dict = {}
    parse_meta = {}
    if text:
        parsed = parse_order_text(text)
        parse_meta = parsed.pop("_meta", {})
        data.update(parsed)
    if fields:
        explicit = dict(fields)
        parse_meta = explicit.pop("_meta", parse_meta) or parse_meta
        data.update(explicit)  # 显式字段覆盖解析结果

    clean = {k: data[k] for k in _ORDER_FIELDS if k in data and data[k] not in (None, "")}
    _coerce_datetimes(clean)
    operator_user = operator if operator and getattr(operator, "is_authenticated", False) else None
    order = Order.objects.create(
        order_no=gen_order_no(timezone.now()),
        channel=channel,
        source=source,
        status=Order.STATUS_PENDING_CONFIRM,
        customer=customer,
        created_by=operator_user,
        raw_text=text,
        parse_meta=parse_meta,
        **clean,
    )
    record_order_event(
        order, "created", actor=operator, to_status=order.status,
        source="ai" if parse_meta.get("source") == "deepseek" else "cs", channel=channel,
    )
    return order


def find_duplicate_orders(*, contact_phone="", origin="", destination="", within_hours=24, limit=5) -> list[Order]:
    """建单查重：近 within_hours 内、同电话或同线路的活跃订单（防客服/客户重复下单）。

    匹配优先级：同电话最强；否则同始发+目的地。已取消订单不计。
    """
    from datetime import timedelta

    if not (contact_phone or (origin and destination)):
        return []
    since = timezone.now() - timedelta(hours=within_hours)
    qs = Order.objects.exclude(status=Order.STATUS_CANCELLED).filter(created_at__gte=since)
    if contact_phone:
        qs = qs.filter(contact_phone=contact_phone)
    else:
        qs = qs.filter(origin=origin, destination=destination)
    return list(qs.select_related("customer", "created_by").order_by("-created_at")[:limit])


def import_orders(rows: list, *, channel: str = Order.CHANNEL_CS, source: str = "", operator=None) -> dict:
    """批量建单：每行一个结构化 fields，逐行建单，失败隔离并返回逐行结果。"""
    if not isinstance(rows, list) or not rows:
        raise AppError("IMPORT_EMPTY", "rows 必须是非空数组。", status=400)
    if len(rows) > MAX_BATCH_SIZE:
        raise AppError("BATCH_TOO_LARGE", f"单次最多导入 {MAX_BATCH_SIZE} 行，请分批。", status=400)
    ok, failed = [], []
    for idx, row in enumerate(rows):
        if not isinstance(row, dict):
            failed.append({"row": idx, "error": "行数据必须是对象"})
            continue
        try:
            order = create_order_from_intake(fields=row, channel=channel, source=source, operator=operator)
            ok.append({"row": idx, "order_no": order.order_no})
        except AppError as exc:
            failed.append({"row": idx, "error": exc.message})
        except Exception as exc:  # noqa: BLE001 - 单行异常不影响整批
            failed.append({"row": idx, "error": str(exc)})
    return {"ok": ok, "failed": failed, "ok_count": len(ok), "failed_count": len(failed)}


def confirm_order(order: Order, *, operator=None) -> Order:
    if order.status not in (Order.STATUS_PENDING_CONFIRM, Order.STATUS_CONFIRMED):
        raise AppError("INVALID_ORDER_STATUS", "仅待确认订单可确认。", status=409)
    prev = order.status
    order.status = Order.STATUS_CONFIRMED
    order.save(update_fields=["status", "updated_at"])
    record_order_event(order, "confirmed", actor=operator, from_status=prev, to_status=order.status, source="cs")
    return order


def pool_order(order: Order, *, operator=None) -> Order:
    """订单进池：确认后投入调度池，实时通知调度。"""
    from apps.core.redis import publish_event

    if order.status not in (Order.STATUS_CONFIRMED, Order.STATUS_PENDING_CONFIRM):
        raise AppError("INVALID_ORDER_STATUS", "仅已确认/待确认订单可进池。", status=409)
    prev = order.status
    order.status = Order.STATUS_POOLED
    order.pooled_at = timezone.now()
    order.save(update_fields=["status", "pooled_at", "updated_at"])
    record_order_event(order, "pooled", actor=operator, from_status=prev, to_status=order.status, source="cs")
    publish_event("order_pooled", {
        "order_no": order.order_no, "origin": order.origin, "destination": order.destination,
        "priority": order.priority, "business_type": order.business_type,
    })
    # 持久化通知：进池→提醒调度员
    from apps.notifications.services import notify_role

    notify_role(
        "dispatcher", category="order_pooled",
        title=f"新订单进池：{order.order_no}",
        body=f"{order.origin}→{order.destination} · {order.get_business_type_display()} · {order.get_priority_display()}",
        level="warning" if order.priority in ("urgent", "vip") else "info",
        link_type="order", link_id=str(order.id),
    )
    return order


def cancel_order(order: Order, *, operator=None) -> Order:
    if order.status in (Order.STATUS_CONVERTED, Order.STATUS_COMPLETED):
        raise AppError("INVALID_ORDER_STATUS", "已派单/已完成订单不可取消。", status=409)
    prev = order.status
    order.status = Order.STATUS_CANCELLED
    order.save(update_fields=["status", "updated_at"])
    record_order_event(order, "cancelled", actor=operator, from_status=prev, to_status=order.status, source="cs")
    return order


def batch_orders(action: str, ids: list, *, operator=None) -> dict:
    """批量操作：confirm/pool/cancel/delete。逐条执行，失败不影响其余。"""
    handlers = {
        "confirm": confirm_order,
        "pool": pool_order,
        "cancel": cancel_order,
        "delete": lambda o: o.delete(),  # 软删
    }
    handler = handlers.get(action)
    if handler is None:
        raise AppError("INVALID_BATCH_ACTION", f"不支持的操作：{action}", status=400)
    if len(ids) > MAX_BATCH_SIZE:
        raise AppError("BATCH_TOO_LARGE", f"单次最多操作 {MAX_BATCH_SIZE} 单，请分批。", status=400)
    ok, failed = [], []
    for order in Order.objects.filter(id__in=ids):
        try:
            handler(order)
            ok.append(order.order_no)
        except AppError as exc:
            failed.append({"order_no": order.order_no, "error": exc.message})
    return {"action": action, "ok": ok, "failed": failed, "ok_count": len(ok)}


def convert_order_to_waybill(order: Order, *, carrier=None, vehicle=None, driver=None,
                             dispatch_type="", operator=None) -> Waybill:
    """订单转运单（人工确认/派单后）。可带承运商/车辆/司机与派单类型，回写订单为已派单。"""
    if order.status in (Order.STATUS_CONVERTED, Order.STATUS_COMPLETED):
        raise AppError("ORDER_ALREADY_CONVERTED", "订单已派单/完成。", status=409)
    if order.status == Order.STATUS_CANCELLED:
        raise AppError("ORDER_CANCELLED", "订单已取消。", status=409)

    route_name = f"{order.origin or '?'}→{order.destination or '?'}"
    waybill = Waybill.objects.create(
        waybill_no=gen_waybill_no(timezone.now()),
        order=order,
        customer=order.customer,
        carrier=carrier,
        vehicle=vehicle,
        driver=driver,
        dispatch_type=dispatch_type,
        route_name=route_name,
        origin=order.origin,
        destination=order.destination,
        cargo_quantity=order.cargo_quantity,
        cargo_weight_ton=order.cargo_weight_ton,
        cargo_volume_cbm=order.cargo_volume_cbm,
        status=Waybill.STATUS_PENDING_DISPATCH,
    )
    order.status = Order.STATUS_CONVERTED
    order.save(update_fields=["status", "updated_at"])
    return waybill
