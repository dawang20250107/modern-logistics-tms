"""多渠道建单：自然语言/微信群消息 → 结构化订单，及订单转运单。

parse_order_text：DeepSeek 可用时用 LLM 抽取，否则规则兜底（离线可测、可用）。
create_order_from_intake：统一入口落 Order（待确认）。
convert_order_to_waybill：人工确认后转运单（高风险写，经确认闭环）。
"""

import json
import random
import re

from django.utils import timezone

from apps.core.exceptions import AppError

from .models import Order, Waybill

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


def _gen_no(prefix: str) -> str:
    return f"{prefix}{timezone.now():%Y%m%d%H%M%S}{random.randint(100, 999)}"


_ORDER_FIELDS = (
    "contact_name", "contact_phone", "origin", "destination", "cargo_desc",
    "cargo_quantity", "cargo_weight_ton", "cargo_volume_cbm",
)


def create_order_from_intake(*, text: str = "", fields: dict | None = None, channel: str = Order.CHANNEL_CS,
                             source: str = "", customer=None, operator=None) -> Order:
    """统一建单入口：有 text 则先 AI/规则解析，落待确认订单。"""
    parse_meta = {}
    data = dict(fields or {})
    if text and not fields:
        parsed = parse_order_text(text)
        parse_meta = parsed.pop("_meta", {})
        data = parsed
    elif "_meta" in data:
        parse_meta = data.pop("_meta", {})

    clean = {k: data[k] for k in _ORDER_FIELDS if k in data and data[k] not in (None, "")}
    order = Order.objects.create(
        order_no=_gen_no("OD"),
        channel=channel,
        source=source,
        status=Order.STATUS_PENDING_CONFIRM,
        customer=customer,
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


def convert_order_to_waybill(order: Order, *, operator=None) -> Waybill:
    """订单转运单（人工确认后）。生成运单并回写订单为已转。"""
    if order.status == Order.STATUS_CONVERTED:
        raise AppError("ORDER_ALREADY_CONVERTED", "订单已转运单。", status=409)
    if order.status == Order.STATUS_CANCELLED:
        raise AppError("ORDER_CANCELLED", "订单已取消。", status=409)

    route_name = f"{order.origin or '?'}→{order.destination or '?'}"
    waybill = Waybill.objects.create(
        waybill_no=_gen_no("WB"),
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
