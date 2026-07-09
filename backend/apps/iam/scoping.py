"""数据权限：按组织子树过滤查询集。

依赖模型上的组织外键（默认字段名 `organization`，见 core.OrgScopedModel）。
组织子树通过 Organization.path 物化路径前缀匹配，避免递归查询。
"""

from django.db.models import Q

from .services import effective_data_scope


def scope_queryset(queryset, user, *, org_field="organization", include_null=False):
    """按用户数据范围过滤查询集（供 Mixin 与函数视图/Agent 工具统一复用）。

    超管/all 档全量；org/self 档限本组织；org_sub 档限组织子树（path 前缀）；
    无组织归属用户：include_null 时仅见无归属记录，否则空集。
    """
    if getattr(user, "is_superuser", False):
        return queryset
    scope = effective_data_scope(user)
    if scope == "all":
        return queryset

    def _scoped(values):
        cond = Q(**{f"{org_field}__in": values})
        if include_null:
            cond |= Q(**{f"{org_field}__isnull": True})
        return queryset.filter(cond)

    org = getattr(user, "organization", None)
    if org is None:
        return queryset.filter(**{f"{org_field}__isnull": True}) if include_null else queryset.none()

    if scope in ("self", "org"):
        return _scoped([org.id])

    if scope == "org_sub":
        from .models import Organization

        prefix = org.path or str(org.id)
        sub_ids = list(
            Organization.objects.filter(path__startswith=prefix).values_list("id", flat=True)
        )
        sub_ids.append(org.id)
        return _scoped(sub_ids)

    return queryset.none()


class OrgScopedQuerysetMixin:
    org_field = "organization"
    # 置 True 时，组织外键为空（无归属）的记录对所有已认证用户可见——
    # 适用于并非每条都挂运单/组织的数据（如无运单的费用），避免误伤合法记录。
    org_scope_include_null = False

    def filter_by_scope(self, queryset):
        return scope_queryset(
            queryset, self.request.user,
            org_field=self.org_field, include_null=self.org_scope_include_null,
        )

    def get_queryset(self):
        return self.filter_by_scope(super().get_queryset())
