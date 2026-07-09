"""登录失败锁定 + 审计落库。

策略：同一用户名在滑动窗口内连续失败达阈值即锁定一段时间，锁定期内即便凭据
正确也拒绝（返回锁定原因与剩余分钟）。计数用 Redis 短缓存（自动过期、抗并发），
每次尝试同时写 LoginAttempt 持久流水，可审计、可导出。成功登录清零计数。
"""

from django.conf import settings
from django.core.cache import cache

_FAIL_KEY = "login:fail:{username}"
_LOCK_KEY = "login:lock:{username}"


def _max_failures() -> int:
    return getattr(settings, "LOGIN_MAX_FAILURES", 5)


def _lockout_seconds() -> int:
    return getattr(settings, "LOGIN_LOCKOUT_MINUTES", 15) * 60


def _window_seconds() -> int:
    return getattr(settings, "LOGIN_FAILURE_WINDOW_MINUTES", 15) * 60


def _norm(username: str) -> str:
    return (username or "").strip().lower()


def lock_remaining_seconds(username: str) -> int:
    """返回该用户名的剩余锁定秒数；未锁定返回 0。"""
    key = _LOCK_KEY.format(username=_norm(username))
    return cache.ttl(key) if _cache_supports_ttl() else (0 if cache.get(key) is None else _lockout_seconds())


def _cache_supports_ttl() -> bool:
    return hasattr(cache, "ttl")


def is_locked(username: str) -> bool:
    return cache.get(_LOCK_KEY.format(username=_norm(username))) is not None


def register_failure(username: str) -> tuple[bool, int]:
    """记一次失败，返回（是否已触发锁定, 剩余可尝试次数）。达阈值时置锁。"""
    uname = _norm(username)
    if not uname:
        return False, _max_failures()
    fail_key = _FAIL_KEY.format(username=uname)
    try:
        count = cache.incr(fail_key)
    except ValueError:
        cache.set(fail_key, 1, _window_seconds())
        count = 1
    limit = _max_failures()
    if count >= limit:
        cache.set(_LOCK_KEY.format(username=uname), 1, _lockout_seconds())
        cache.delete(fail_key)
        return True, 0
    return False, max(0, limit - count)


def clear_failures(username: str) -> None:
    uname = _norm(username)
    cache.delete(_FAIL_KEY.format(username=uname))
    cache.delete(_LOCK_KEY.format(username=uname))


def unlock(username: str) -> None:
    """管理员手动解锁。"""
    clear_failures(username)


def record_attempt(*, username, user=None, success, result, request=None):
    """写一条登录审计流水。异常不阻断登录主流程。"""
    from .models import LoginAttempt

    ip, ua = None, ""
    if request is not None:
        ip = _client_ip(request)
        ua = (request.META.get("HTTP_USER_AGENT") or "")[:255]
    try:
        LoginAttempt.objects.create(
            username=(username or "")[:150], user=user if getattr(user, "pk", None) else None,
            success=success, result=result, ip=ip, user_agent=ua,
        )
    except Exception:  # noqa: BLE001 — 审计失败不应阻断登录
        pass


def _client_ip(request) -> str | None:
    xff = request.META.get("HTTP_X_FORWARDED_FOR")
    if xff:
        return xff.split(",")[0].strip()
    return request.META.get("REMOTE_ADDR")
