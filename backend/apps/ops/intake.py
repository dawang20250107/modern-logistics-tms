"""多渠道建单：自然语言/微信群消息 → 结构化订单，及订单转运单。

parse_order_text：DeepSeek 可用时用 LLM 抽取，否则规则兜底（离线可测、可用）。
create_order_from_intake：统一入口落 Order（待确认）。
convert_order_to_waybill：人工确认后转运单（高风险写，经确认闭环）。
"""

import json
import re

from django.utils import timezone

from apps.core.exceptions import AppError

from .models import Order, Waybill
from .numbering import order_no as gen_order_no
from .numbering import waybill_no as gen_waybill_no

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
    return order


def confirm_order(order: Order) -> Order:
    if order.status not in (Order.STATUS_PENDING_CONFIRM, Order.STATUS_CONFIRMED):
        raise AppError("INVALID_ORDER_STATUS", "仅待确认订单可确认。", status=409)
    order.status = Order.STATUS_CONFIRMED
    order.save(update_fields=["status", "updated_at"])
    return order


def pool_order(order: Order) -> Order:
    """订单进池：确认后投入调度池，实时通知调度。"""
    from apps.core.redis import publish_event

    if order.status not in (Order.STATUS_CONFIRMED, Order.STATUS_PENDING_CONFIRM):
        raise AppError("INVALID_ORDER_STATUS", "仅已确认/待确认订单可进池。", status=409)
    order.status = Order.STATUS_POOLED
    order.pooled_at = timezone.now()
    order.save(update_fields=["status", "pooled_at", "updated_at"])
    publish_event("order_pooled", {
        "order_no": order.order_no, "origin": order.origin, "destination": order.destination,
        "priority": order.priority, "business_type": order.business_type,
    })
    return order


def cancel_order(order: Order) -> Order:
    if order.status in (Order.STATUS_CONVERTED, Order.STATUS_COMPLETED):
        raise AppError("INVALID_ORDER_STATUS", "已派单/已完成订单不可取消。", status=409)
    order.status = Order.STATUS_CANCELLED
    order.save(update_fields=["status", "updated_at"])
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
    ok, failed = [], []
    for order in Order.objects.filter(id__in=ids):
        try:
            handler(order)
            ok.append(order.order_no)
        except AppError as exc:
            failed.append({"order_no": order.order_no, "error": exc.message})
    return {"action": action, "ok": ok, "failed": failed, "ok_count": len(ok)}


def convert_order_to_waybill(order: Order, *, operator=None) -> Waybill:
    """订单转运单（人工确认后）。生成运单并回写订单为已转。"""
    if order.status == Order.STATUS_CONVERTED:
        raise AppError("ORDER_ALREADY_CONVERTED", "订单已转运单。", status=409)
    if order.status == Order.STATUS_CANCELLED:
        raise AppError("ORDER_CANCELLED", "订单已取消。", status=409)

    route_name = f"{order.origin or '?'}→{order.destination or '?'}"
    waybill = Waybill.objects.create(
        waybill_no=gen_waybill_no(timezone.now()),
        order=order,
        customer=order.customer,
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
