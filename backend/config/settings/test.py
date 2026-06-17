"""测试配置：与运行中的应用及彼此隔离。

- 本地内存缓存（避免与运行中应用共享 Redis，导致幂等键串扰）。
- 关闭限流（避免请求计数引发偶发 429）。
"""

from .local import *  # noqa: F401,F403

SECRET_KEY = "test-insecure-secret-key-min-32-bytes-long"  # noqa: S105
CACHES = {
    "default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache", "LOCATION": "tms-test"}
}
SESSION_ENGINE = "django.contrib.sessions.backends.db"
REST_FRAMEWORK = {  # noqa: F405
    **REST_FRAMEWORK,  # noqa: F405
    "DEFAULT_THROTTLE_CLASSES": [],
    "DEFAULT_THROTTLE_RATES": {},
}

# 测试内联执行 Celery 任务（OCR/落库等），保证确定性
CELERY_TASK_ALWAYS_EAGER = True
CELERY_TASK_EAGER_PROPAGATES = True
