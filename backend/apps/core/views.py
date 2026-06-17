"""健康/就绪探针：纯 Django 视图，无鉴权、无信封，供编排与 LB 使用。

- /healthz  存活：进程在跑即 200。
- /readyz   就绪：依赖（DB、Redis）可用才 200，否则 503，供流量摘除。
"""

from django.core.cache import cache
from django.db import connections
from django.http import JsonResponse


def healthz(_request):
    return JsonResponse({"status": "ok"})


def readyz(_request):
    checks = {}
    healthy = True

    try:
        with connections["default"].cursor() as cursor:
            cursor.execute("SELECT 1")
            cursor.fetchone()
        checks["database"] = "ok"
    except Exception as exc:  # noqa: BLE001
        checks["database"] = f"error: {exc.__class__.__name__}"
        healthy = False

    try:
        cache.set("readyz:probe", "1", timeout=5)
        cache.get("readyz:probe")
        checks["cache"] = "ok"
    except Exception as exc:  # noqa: BLE001
        checks["cache"] = f"error: {exc.__class__.__name__}"
        healthy = False

    return JsonResponse(
        {"status": "ok" if healthy else "degraded", "checks": checks},
        status=200 if healthy else 503,
    )
