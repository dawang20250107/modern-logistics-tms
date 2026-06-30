"""运满满 / 满帮开放平台：调车运费比价接口。

接口：{YMM_BASE_URL}/apis/openapi/workbench
配置凭证（YMM_APP_KEY/SECRET/ACCESS_TOKEN）后发起真实签名请求；未配置或网络不可达时
回退为离线参考价（明确标注 source=offline），保证派单比价始终可用、可测。
"""

import hashlib
import hmac
import logging
import time

import httpx
from django.conf import settings

logger = logging.getLogger("integrations.ymm")

_WORKBENCH_PATH = "/apis/openapi/workbench"


def _sign(params: dict, secret: str) -> str:
    """满帮开放平台签名：按键排序拼接后 HMAC-SHA256（凭证缺失时返回空串）。"""
    if not secret:
        return ""
    base = "&".join(f"{k}={params[k]}" for k in sorted(params) if params[k] != "")
    return hmac.new(secret.encode(), base.encode(), hashlib.sha256).hexdigest().upper()


def _configured() -> bool:
    return bool(settings.YMM_APP_KEY and settings.YMM_APP_SECRET and settings.YMM_ACCESS_TOKEN)


def _offline_estimate(origin: str, destination: str, weight_ton, volume_cbm) -> dict:
    """离线参考价：基础价 + 吨位/体积加权的稳定估算（未接入运满满时的兜底）。"""
    w = float(weight_ton or 0)
    v = float(volume_cbm or 0)
    base = 600.0
    chargeable = max(w, v * 0.33)  # 抛重折算
    avg = round(base + chargeable * 180.0, 2)
    return {
        "source": "offline",
        "provider": "运满满(离线参考)",
        "route": f"{origin or '?'}→{destination or '?'}",
        "low": round(avg * 0.9, 2),
        "avg": avg,
        "high": round(avg * 1.15, 2),
        "currency": "CNY",
        "note": "未配置运满满凭证或暂不可达，返回离线参考价。",
    }


def _parse_response(data: dict, origin: str, destination: str) -> dict:
    """解析运满满返回的比价数据（字段名按平台返回容错取值）。"""
    body = data.get("data") or data.get("result") or data
    low = body.get("lowPrice") or body.get("minPrice") or body.get("low")
    high = body.get("highPrice") or body.get("maxPrice") or body.get("high")
    avg = body.get("avgPrice") or body.get("price") or (
        (float(low) + float(high)) / 2 if low and high else None
    )
    return {
        "source": "ymm",
        "provider": "运满满",
        "route": f"{origin or '?'}→{destination or '?'}",
        "low": float(low) if low is not None else None,
        "avg": float(avg) if avg is not None else None,
        "high": float(high) if high is not None else None,
        "currency": "CNY",
        "note": "运满满开放平台实时比价。",
    }


def freight_quote(origin: str, destination: str, *, weight_ton=0, volume_cbm=0, vehicle_type="") -> dict:
    """调车运费比价：优先调用运满满开放平台，结合本地历史成交价进行 AI 智能估价。"""
    from datetime import timedelta

    from django.db.models import Avg
    from django.utils import timezone

    from apps.ops.models import Order

    # 1. 获取满帮/离线估价作为基础市场价信号
    market_quote = None
    if not _configured():
        market_quote = _offline_estimate(origin, destination, weight_ton, volume_cbm)
    else:
        params = {
            "appKey": settings.YMM_APP_KEY,
            "accessToken": settings.YMM_ACCESS_TOKEN,
            "timestamp": str(int(time.time() * 1000)),
            "method": "freight.price.compare",
            "startAddress": origin,
            "endAddress": destination,
            "weight": str(weight_ton or ""),
            "volume": str(volume_cbm or ""),
            "vehicleType": vehicle_type or "",
        }
        params["sign"] = _sign(params, settings.YMM_APP_SECRET)
        url = f"{settings.YMM_BASE_URL}{_WORKBENCH_PATH}"
        try:
            resp = httpx.post(url, json=params, timeout=settings.YMM_TIMEOUT_SECONDS)
            resp.raise_for_status()
            payload = resp.json()
            if str(payload.get("code", "0")) in ("0", "200", "success"):
                market_quote = _parse_response(payload, origin, destination)
        except Exception as exc:  # noqa: BLE001
            logger.warning("ymm freight_quote API failed, fallback to offline: %s", exc)

    if not market_quote:
        market_quote = _offline_estimate(origin, destination, weight_ton, volume_cbm)

    # 2. 查询本地历史 90 天内该线路已完成订单的平均成交价（AI 数据对齐）
    hist_avg = None
    try:
        past_days = timezone.now() - timedelta(days=90)
        history = Order.objects.filter(
            origin__icontains=origin,
            destination__icontains=destination,
            status="completed",
            created_at__gte=past_days,
            quoted_amount__gt=0
        ).aggregate(avg_price=Avg("quoted_amount"))
        hist_avg = history.get("avg_price")
    except Exception as exc:  # noqa: BLE001 — 容错，不阻断主链路
        logger.warning("Failed to query historical freight average: %s", exc)

    # 3. AI 智能价格混合插值算法 (α * historical + β * market)
    market_avg = float(market_quote.get("avg") or 0)
    
    if hist_avg is not None and market_avg > 0:
        hist_avg = float(hist_avg)
        # 混合加权：60% 历史自营合同价 + 40% 满帮公网即时行情价
        ai_avg = round(0.6 * hist_avg + 0.4 * market_avg, 2)
        market_quote["avg"] = ai_avg
        market_quote["low"] = round(ai_avg * 0.9, 2)
        market_quote["high"] = round(ai_avg * 1.12, 2)
        market_quote["note"] = f"AI 智能混合估值（结合历史成交价 {hist_avg}元 与市场比价）"
    else:
        market_quote["note"] = market_quote.get("note", "") + " (无本地历史数据，纯市场估值)"

    return market_quote
