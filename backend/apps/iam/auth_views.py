"""带审计与失败锁定的登录视图，替换裸 TokenObtainPairView。

- 登录前：命中锁定直接拒绝（423），记审计。
- 凭据错误：累加失败计数，达阈值置锁；区分「凭据错误」与「账号停用」。
- 登录成功：清零失败计数，记成功审计（last_login 由 simplejwt 更新）。
"""

from django.contrib.auth import get_user_model
from rest_framework import status, viewsets
from rest_framework.decorators import action
from rest_framework.exceptions import APIException, AuthenticationFailed
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework_simplejwt.exceptions import InvalidToken, TokenError
from rest_framework_simplejwt.views import TokenObtainPairView

from .login_guard import (
    clear_failures,
    is_locked,
    lock_remaining_seconds,
    record_attempt,
    register_failure,
    unlock,
)
from .models import LoginAttempt
from .permissions import HasPermission
from .serializers import LoginAttemptSerializer

# 登录审计属安全敏感数据：读取需组织查看权、解锁需角色权限管理权（与组织中台一致）
PERM_AUDIT_VIEW = "org.view"
PERM_AUDIT_UNLOCK = "org.rbac"


class AccountLocked(APIException):
    status_code = 423
    default_code = "account_locked"
    default_detail = "登录失败次数过多，账号已锁定，请稍后重试。"


def _raise_locked(username, request):
    """记锁定审计并抛 423（经统一异常处理器包成 {success:false,error}）。"""
    secs = lock_remaining_seconds(username)
    mins = max(1, (secs + 59) // 60) if secs else 1
    record_attempt(
        username=username, success=False, result=LoginAttempt.RESULT_LOCKED, request=request
    )
    raise AccountLocked(f"登录失败次数过多，账号已锁定，请约 {mins} 分钟后重试。")


class AuditedTokenObtainPairView(TokenObtainPairView):
    def post(self, request, *args, **kwargs):
        username = request.data.get("username", "")
        if is_locked(username):
            _raise_locked(username, request)

        serializer = self.get_serializer(data=request.data)
        try:
            serializer.is_valid(raise_exception=True)
        except (AuthenticationFailed, InvalidToken, TokenError) as exc:
            result = self._classify_failure(username)
            locked, _remaining = register_failure(username)
            record_attempt(username=username, success=False, result=result, request=request)
            if locked:
                _raise_locked(username, request)
            raise exc

        user = getattr(serializer, "user", None)
        clear_failures(username)
        record_attempt(
            username=username, user=user, success=True,
            result=LoginAttempt.RESULT_SUCCESS, request=request,
        )
        return Response(serializer.validated_data, status=status.HTTP_200_OK)

    @staticmethod
    def _classify_failure(username: str) -> str:
        """区分账号停用与凭据错误，便于审计定位。"""
        user = get_user_model().objects.filter(username=username).first()
        if user is not None and not user.is_active:
            return LoginAttempt.RESULT_INACTIVE
        return LoginAttempt.RESULT_BAD_CREDENTIALS


class LoginAuditViewSet(viewsets.ReadOnlyModelViewSet):
    """登录审计台账：谁在何时何地登录成功/失败，支持按用户名/结果/成败筛查。

    附解锁动作：安全管理员可手动解除某用户名的失败锁定（留痕在审计流水外）。
    """

    queryset = LoginAttempt.objects.select_related("user").all()
    serializer_class = LoginAttemptSerializer
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = {"read": PERM_AUDIT_VIEW, "unlock": PERM_AUDIT_UNLOCK}
    search_fields = ["username", "ip"]
    filterset_fields = ["success", "result", "username"]
    ordering_fields = ["created_at"]

    @action(detail=False, methods=["post"])
    def unlock(self, request):
        """解除某用户名的登录失败锁定：{"username": "..."}。"""
        username = (request.data.get("username") or "").strip()
        if not username:
            return Response({"detail": "缺少 username"}, status=status.HTTP_400_BAD_REQUEST)
        unlock(username)
        return Response({"username": username, "unlocked": True})
