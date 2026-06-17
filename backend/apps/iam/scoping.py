"""数据权限：按组织子树过滤查询集。

依赖模型上的组织外键（默认字段名 `organization`，见 core.OrgScopedModel）。
组织子树通过 Organization.path 物化路径前缀匹配，避免递归查询。
"""

from .services import effective_data_scope


class OrgScopedQuerysetMixin:
    org_field = "organization"

    def filter_by_scope(self, queryset):
        user = self.request.user
        if getattr(user, "is_superuser", False):
            return queryset
        scope = effective_data_scope(user)
        if scope == "all":
            return queryset

        org = getattr(user, "organization", None)
        if org is None:
            return queryset.none()

        if scope in ("self", "org"):
            return queryset.filter(**{self.org_field: org})

        if scope == "org_sub":
            from .models import Organization

            prefix = (org.path or str(org.id))
            sub_ids = list(
                Organization.objects.filter(path__startswith=prefix).values_list("id", flat=True)
            )
            sub_ids.append(org.id)
            return queryset.filter(**{f"{self.org_field}__in": sub_ids})

        return queryset.none()

    def get_queryset(self):
        return self.filter_by_scope(super().get_queryset())
