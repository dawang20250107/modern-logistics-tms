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


# ── 组织中台：组织树 / 人头汇总 / 账号移交 ──────────────────────────


def build_org_tree(organizations, headcount: dict | None = None) -> list[dict]:
    """把扁平组织列表组装成嵌套树；可选叠加各组织（含子树）的人头数。

    headcount: {org_id: 本组织直属在职员工数}。返回节点带 `direct_headcount`
    与 `total_headcount`（自身 + 全部子树），便于前端一棵树看清编制分布。
    """
    headcount = headcount or {}
    nodes: dict = {}
    for org in organizations:
        nodes[org.id] = {
            "id": str(org.id),
            "code": org.code,
            "name": org.name,
            "short_name": org.short_name,
            "type": org.type,
            "type_label": org.get_type_display(),
            "org_property": org.org_property,
            "org_property_label": org.get_org_property_display(),
            "manager_name": org.manager_name,
            "is_active": org.is_active,
            "parent_id": str(org.parent_id) if org.parent_id else None,
            "direct_headcount": headcount.get(org.id, 0),
            "total_headcount": headcount.get(org.id, 0),
            "children": [],
        }

    roots: list = []
    for org in organizations:
        node = nodes[org.id]
        parent = nodes.get(org.parent_id) if org.parent_id else None
        if parent is not None:
            parent["children"].append(node)
        else:
            roots.append(node)

    # 自底向上累加子树人头：按 path 深度倒序保证子节点先于父节点处理
    for org in sorted(organizations, key=lambda o: (o.path or "").count("/"), reverse=True):
        node = nodes[org.id]
        parent = nodes.get(org.parent_id) if org.parent_id else None
        if parent is not None:
            parent["total_headcount"] += node["total_headcount"]
    return roots


def handover_account(from_employee, to_employee, *, operator=None, reason="", disable=True):
    """执行账号移交：下属改挂、所辖部门负责人改派，可选停用原账号。返回 AccountHandover。"""
    from django.db import transaction

    from .models import AccountHandover, Department, Employee

    if from_employee.id == to_employee.id:
        raise ValueError("移交人与接收人不能相同")

    with transaction.atomic():
        moved_reports = Employee.objects.filter(supervisor=from_employee).update(
            supervisor=to_employee
        )
        moved_departments = Department.objects.filter(manager=from_employee).update(
            manager=to_employee
        )
        disabled = False
        if disable:
            from_employee.status = "left"
            from_employee.save(update_fields=["status", "updated_at"])
            if from_employee.user_id:
                from_employee.user.is_active = False
                from_employee.user.save(update_fields=["is_active"])
                disabled = True
        record = AccountHandover.objects.create(
            from_employee=from_employee,
            to_employee=to_employee,
            operator=operator if getattr(operator, "is_authenticated", False) else None,
            reason=reason,
            moved_reports=moved_reports,
            moved_departments=moved_departments,
            disabled_account=disabled,
        )
    return record
