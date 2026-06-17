"""原生 Redis 客户端（用于队列/发布订阅等 Django 缓存接口不覆盖的操作）。"""

import json

import redis
from django.conf import settings

_client = None

EVENT_CHANNEL = "tms:events"


def get_redis() -> "redis.Redis":
    global _client
    if _client is None:
        _client = redis.from_url(settings.REDIS_URL, decode_responses=True)
    return _client


def publish_event(event_type: str, data: dict) -> None:
    """向实时事件通道发布一条消息（供 SSE 转发）。失败不影响主流程。"""
    try:
        get_redis().publish(EVENT_CHANNEL, json.dumps({"type": event_type, "data": data}, default=str))
    except Exception:  # noqa: BLE001 - 实时通道非关键路径
        pass
