"""权限与数据范围计算（带 Redis 短缓存，面向高并发）。

支持两类主体：
- 真实用户（accounts.User）：权限来自 角色→权限点；数据范围取角色中最宽档。
- 服务主体（API-Key 的 ServicePrincipal）：权限来自 api_key.scopes。
"""

from django.core.cache import cache

PERM_CACHE_TTL = 60
_SCOPE_RANK = {"self": 0, "org": 1, "org_sub": 2, "all": 3}


def _is_authenticated(user) -> bool:
    return bool(user and getattr(user, "is_authenticated", False))


def effective_permissions(user) -> set[str]:
    if not _is_authenticated(user):
        return set()
    if getattr(user, "is_superuser", False):
        return {"*"}

    api_key = getattr(user, "api_key", None)
    if api_key is not None:
        return {code for code in (api_key.scopes or "").split(",") if code}

    cache_key = f"iam:perms:{user.pk}"
    cached = cache.get(cache_key)
    if cached is not None:
        return set(cached)

    from .models import RoleAssignment

    codes = set(
        RoleAssignment.objects.filter(user=user, role__is_active=True)
        .values_list("role__permissions__code", flat=True)
    )
    codes.discard(None)
    cache.set(cache_key, list(codes), PERM_CACHE_TTL)
    return codes


def has_perm(user, code: str) -> bool:
    perms = effective_permissions(user)
    return "*" in perms or code in perms


def effective_data_scope(user) -> str:
    """返回数据范围档：self / org / org_sub / all。"""
    if getattr(user, "is_superuser", False):
        return "all"
    api_key = getattr(user, "api_key", None)
    if api_key is not None:
        return "org_sub" if getattr(user, "organization", None) else "all"

    if not _is_authenticated(user):
        return "self"

    from .models import RoleAssignment

    scopes = (
        RoleAssignment.objects.filter(user=user, role__is_active=True)
        .values_list("role__data_scope", flat=True)
    )
    best = "self"
    for scope in scopes:
        if _SCOPE_RANK.get(scope, 0) > _SCOPE_RANK[best]:
            best = scope
    return best
