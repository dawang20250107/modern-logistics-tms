"""RequestID、访问日志与幂等中间件。"""

import logging
import time
import uuid

from django.core.cache import cache
from django.http import HttpResponse

from .context import request_id_var

logger = logging.getLogger("apps.access")

_SILENT_PATHS = {"/healthz", "/readyz", "/metrics"}
IDEMPOTENCY_TTL = 600


class RequestIDMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        rid = request.META.get("HTTP_X_REQUEST_ID") or uuid.uuid4().hex
        request.request_id = rid
        token = request_id_var.set(rid)
        try:
            response = self.get_response(request)
        finally:
            request_id_var.reset(token)
        response["X-Request-ID"] = rid
        return response


class AccessLogMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        start = time.perf_counter()
        response = self.get_response(request)
        if request.path in _SILENT_PATHS:
            return response
        duration_ms = round((time.perf_counter() - start) * 1000, 2)
        logger.info(
            "%s %s -> %s (%sms)",
            request.method,
            request.get_full_path(),
            response.status_code,
            duration_ms,
            extra={
                "extra_fields": {
                    "method": request.method,
                    "path": request.path,
                    "status": response.status_code,
                    "duration_ms": duration_ms,
                }
            },
        )
        return response


class IdempotencyMiddleware:
    """对带 `Idempotency-Key` 的 POST 提供幂等：首个成功响应缓存于 Redis，重复请求直接回放。"""

    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        key = request.headers.get("Idempotency-Key")
        if request.method != "POST" or not key:
            return self.get_response(request)

        cache_key = f"idem:{key}:{request.path}"
        cached = cache.get(cache_key)
        if cached is not None:
            resp = HttpResponse(
                cached["body"], status=cached["status"], content_type=cached["content_type"]
            )
            resp["Idempotent-Replay"] = "true"
            return resp

        response = self.get_response(request)
        if 200 <= response.status_code < 300:
            try:
                body = response.content
            except Exception:  # noqa: BLE001 - 渲染延迟
                if hasattr(response, "render"):
                    response.render()
                    body = response.content
                else:
                    body = b""
            cache.set(
                cache_key,
                {
                    "body": body,
                    "status": response.status_code,
                    "content_type": response.get("Content-Type", "application/json"),
                },
                IDEMPOTENCY_TTL,
            )
        return response
