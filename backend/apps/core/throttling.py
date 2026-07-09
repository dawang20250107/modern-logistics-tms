"""基于 Redis 缓存计数的限流。

- Burst/Sustained：按用户两档限流。
- ApiKey：按对外 API 密钥单独配额（非 API-Key 请求不受影响）。
"""

from rest_framework.settings import api_settings
from rest_framework.throttling import (
    ScopedRateThrottle,
    SimpleRateThrottle,
    UserRateThrottle,
)


class BurstRateThrottle(UserRateThrottle):
    scope = "burst"


class SustainedRateThrottle(UserRateThrottle):
    scope = "sustained"


class ApiKeyRateThrottle(SimpleRateThrottle):
    scope = "apikey"

    def get_cache_key(self, request, view):
        api_key = getattr(request.user, "api_key", None)
        if api_key is None:
            return None  # 仅对 API-Key 主体生效
        return f"throttle_apikey_{api_key.key_id}"


class OptionalScopedRateThrottle(ScopedRateThrottle):
    """按视图 throttle_scope 限流；未配置该 scope 速率时不限流。

    尊重测试/本地把 DEFAULT_THROTTLE_RATES 置空以关闭限流的约定，避免因缺速率
    抛 ImproperlyConfigured。用于 AI/LLM 等按 scope 限流的端点。
    """

    def get_rate(self):
        if not getattr(self, "scope", None):
            return None
        return api_settings.DEFAULT_THROTTLE_RATES.get(self.scope)


class DriverLoginRateThrottle(SimpleRateThrottle):
    """司机端登录按来源 IP 限流，防止暴力枚举身份证后 6 位。

    未配置该 scope 的速率时返回 None（限流停用），尊重测试环境关闭限流的约定。
    """

    scope = "driver_login"

    def get_rate(self):
        return api_settings.DEFAULT_THROTTLE_RATES.get(self.scope)

    def get_cache_key(self, request, view):
        return f"throttle_driver_login_{self.get_ident(request)}"
