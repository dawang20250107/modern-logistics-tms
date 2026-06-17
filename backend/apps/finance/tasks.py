"""Webhook 异步投递（HMAC 签名 + 有限重试）。"""

import hashlib
import hmac
import json
import time

import httpx
from celery import shared_task

from .models import WebhookDelivery


def _sign(secret: str, timestamp: str, body: str) -> str:
    return hmac.new((secret or "").encode(), f"{timestamp}.{body}".encode(), hashlib.sha256).hexdigest()


@shared_task(name="finance.deliver_webhook")
def deliver_webhook(delivery_id: str, max_attempts: int = 3) -> bool:
    delivery = WebhookDelivery.objects.filter(id=delivery_id).select_related("webhook").first()
    if delivery is None:
        return False
    webhook = delivery.webhook
    body = json.dumps({"event": delivery.event_type, "data": delivery.payload}, default=str, ensure_ascii=False)
    ts = str(int(time.time()))
    headers = {
        "Content-Type": "application/json",
        "X-Event": delivery.event_type,
        "X-Timestamp": ts,
        "X-Signature": _sign(webhook.secret, ts, body),
    }

    delivery.attempts += 1
    ok = False
    code = None
    try:
        resp = httpx.post(webhook.target_url, content=body.encode(), headers=headers, timeout=10)
        code = resp.status_code
        ok = 200 <= code < 300
    except httpx.HTTPError:
        ok = False

    delivery.response_code = code
    delivery.status = "success" if ok else "failed"
    delivery.save(update_fields=["attempts", "response_code", "status", "updated_at"])

    if not ok and delivery.attempts < max_attempts:
        deliver_webhook.apply_async(args=[delivery_id], countdown=10)
    return ok
