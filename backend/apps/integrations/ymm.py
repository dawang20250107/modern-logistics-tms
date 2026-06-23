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
    """调车运费比价：优先调用运满满开放平台，失败回退离线参考价。"""
    if not _configured():
        return _offline_estimate(origin, destination, weight_ton, volume_cbm)

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
        if str(payload.get("code", "0")) not in ("0", "200", "success"):
            logger.warning("ymm freight_quote non-ok: %s", payload.get("message"))
            return _offline_estimate(origin, destination, weight_ton, volume_cbm)
        return _parse_response(payload, origin, destination)
    except Exception as exc:  # noqa: BLE001 — 外部接口失败回退离线参考价，不阻断派单
        logger.warning("ymm freight_quote failed: %s", exc)
        return _offline_estimate(origin, destination, weight_ton, volume_cbm)
