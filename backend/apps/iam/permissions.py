"""基于权限点的 DRF 权限类。

视图声明 `required_permissions`（按 action 映射的权限点，或单个字符串）；
未声明时退化为"已认证即可"，不影响既有端点。
"""

from rest_framework.permissions import SAFE_METHODS, BasePermission

from .services import has_perm


class HasPermission(BasePermission):
    message = "缺少所需权限。"

    def has_permission(self, request, view):
        user = request.user
        if not (user and getattr(user, "is_authenticated", False)):
            return False
        required = getattr(view, "required_permissions", None)
        code = self._resolve(view, request, required)
        if code is None:
            return True
        return has_perm(user, code)

    @staticmethod
    def _resolve(view, request, required):
        if not required:
            return None
        if isinstance(required, str):
            return required
        action = getattr(view, "action", None)
        if action and action in required:
            return required[action]
        if request.method in SAFE_METHODS and "read" in required:
            return required["read"]
        if request.method not in SAFE_METHODS and "write" in required:
            return required["write"]
        return required.get("default")
