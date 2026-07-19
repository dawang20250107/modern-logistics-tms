"""自助账户能力（个人中心）：注册 / 改密 / 我的登录记录。

设计取向：
- 注册只创建基础账号（is_active=True），**不**自带组织/角色/权限——组织与角色一律
  由管理员在组织中台分配，杜绝自助注册即提权。注册成功即签发 JWT（自动登录），
  前端引导至个人中心，明确提示"待管理员分配组织与角色"。
- 改密需校验当前密码并过 Django 密码强度校验；成功后当前会话令牌仍有效（不强制登出）。
- 登录记录只返回本人流水（复用 LoginAttempt 审计表），供安全自查。
"""

from rest_framework import status
from rest_framework.permissions import AllowAny, IsAuthenticated
from rest_framework.response import Response
from rest_framework.throttling import ScopedRateThrottle
from rest_framework.views import APIView
from rest_framework_simplejwt.tokens import RefreshToken

from .login_guard import record_attempt
from .models import LoginAttempt
from .serializers import (
    ChangePasswordSerializer,
    LoginAttemptSerializer,
    RegisterSerializer,
)


class RegisterView(APIView):
    """自助注册并自动登录（返回 access/refresh）。限流防刷。"""

    permission_classes = [AllowAny]
    throttle_classes = [ScopedRateThrottle]
    throttle_scope = "register"

    def post(self, request):
        serializer = RegisterSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        user = serializer.save()
        record_attempt(
            username=user.username, user=user, success=True,
            result=LoginAttempt.RESULT_SUCCESS, request=request,
        )
        refresh = RefreshToken.for_user(user)
        return Response(
            {"access": str(refresh.access_token), "refresh": str(refresh)},
            status=status.HTTP_201_CREATED,
        )


class ChangePasswordView(APIView):
    permission_classes = [IsAuthenticated]

    def post(self, request):
        serializer = ChangePasswordSerializer(data=request.data, context={"request": request})
        serializer.is_valid(raise_exception=True)
        user = request.user
        user.set_password(serializer.validated_data["new_password"])
        user.save(update_fields=["password"])
        return Response({"detail": "密码已更新"})


class MyLoginHistoryView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        attempts = LoginAttempt.objects.filter(user=request.user).order_by("-created_at")[:20]
        return Response(LoginAttemptSerializer(attempts, many=True).data)
