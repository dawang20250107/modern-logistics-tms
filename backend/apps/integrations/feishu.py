"""飞书开放平台接入（预留）：Bot 消息卡片 + 多维表格双向同步。

卡片构造为真实可用的飞书 interactive card 结构；推送与多维表同步为预留
（配置 FEISHU_APP_ID/SECRET 后实现真实调用），当前不发起网络请求。

对应「运链」方案四类飞书 Bot 卡片：
  ① 新增车需求卡片  ② 调度结果提交卡片  ③ 异常预警卡片  ④ 转人工请求卡片
"""

import logging

from django.conf import settings

logger = logging.getLogger("integrations.feishu")

RESERVED = {"status": "reserved", "channel": "feishu"}


def configured() -> bool:
    return bool(settings.FEISHU_APP_ID and settings.FEISHU_APP_SECRET)


def _card(title: str, color: str, fields: list[tuple[str, str]], actions: list[dict] | None = None) -> dict:
    """构造飞书 interactive 卡片（标题 + 字段两列 + 可选按钮）。"""
    elements: list[dict] = [{
        "tag": "div",
        "fields": [
            {"is_short": True, "text": {"tag": "lark_md", "content": f"**{k}**\n{v}"}}
            for k, v in fields
        ],
    }]
    if actions:
        elements.append({"tag": "action", "actions": actions})
    return {
        "config": {"wide_screen_mode": True},
        "header": {"template": color, "title": {"tag": "plain_text", "content": title}},
        "elements": elements,
    }


def new_demand_card(order) -> dict:
    """① 新增车需求卡片：AI 确认需求完整后推送调度员。"""
    return _card(
        "🚚 新增调车需求", "blue",
        [
            ("订单号", order.order_no),
            ("线路", f"{order.origin or '?'}→{order.destination or '?'}"),
            ("货物", order.cargo_desc or "见明细"),
            ("货量", f"{order.cargo_weight_ton}吨 / {order.cargo_quantity}件"),
            ("联系人", f"{order.contact_name} {order.contact_phone}".strip()),
            ("期望提货", str(order.expected_pickup_at or "—")),
        ],
        actions=[{
            "tag": "button", "type": "primary",
            "text": {"tag": "plain_text", "content": "填写调度结果"},
            "value": {"action": "dispatch_fill", "order_no": order.order_no},
        }],
    )


def dispatch_result_card(order, *, vehicle="", driver="", carrier="") -> dict:
    """② 调度结果提交卡片：调度员填写后返回 AI 系统。"""
    return _card(
        "✅ 调度结果", "green",
        [
            ("订单号", order.order_no),
            ("承运商", carrier or "—"),
            ("车辆", vehicle or "—"),
            ("司机", driver or "—"),
        ],
    )


def exception_alert_card(exc) -> dict:
    """③ 异常预警卡片：司机长时间未确认/未注册或在途异常时触发。"""
    return _card(
        "⚠️ 异常预警", "red",
        [
            ("类型", getattr(exc, "exception_type", "")),
            ("级别", getattr(exc, "get_level_display", lambda: getattr(exc, "level", ""))()),
            ("运单", exc.waybill.waybill_no if getattr(exc, "waybill_id", None) else "—"),
            ("描述", getattr(exc, "description", "") or "—"),
        ],
        actions=[{
            "tag": "button", "type": "danger",
            "text": {"tag": "plain_text", "content": "处理异常"},
            "value": {"action": "handle_exception", "id": str(getattr(exc, "id", ""))},
        }],
    )


def transfer_human_card(*, reason="", context="") -> dict:
    """④ 转人工请求卡片：AI 无法处理时推送客服主管。"""
    return _card(
        "🙋 转人工请求", "orange",
        [("原因", reason or "—"), ("上下文", context or "—")],
        actions=[{
            "tag": "button", "type": "primary",
            "text": {"tag": "plain_text", "content": "接管会话"},
            "value": {"action": "take_over"},
        }],
    )


def push_card(card: dict, *, receive_id: str = "", receive_id_type: str = "chat_id") -> dict:
    """推送卡片到飞书（预留）。配置应用凭证后在此调用 im/v1/messages 发送。"""
    logger.info("feishu.push_card reserved: %s", card.get("header", {}).get("title", {}).get("content", ""))
    return {**RESERVED, "action": "push_card", "configured": configured()}


def sync_to_bitable(table: str, record: dict) -> dict:
    """多维表格双向同步——写入（预留）。"""
    logger.info("feishu.sync_to_bitable reserved: table=%s", table)
    return {**RESERVED, "action": "sync_to_bitable", "table": table}
