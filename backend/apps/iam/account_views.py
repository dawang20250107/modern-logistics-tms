"""自助账户能力（个人中心）：注册 / 改密 / 我的登录记录。

设计取向：
- 注册只创建基础账号（is_active=True），**不**自带组织/角色/权限——组织与角色一律
  由管理员在组织中台分配，杜绝自助注册即提权。注册成功即签发 JWT（自动登录），
  前端引导至个人中心，明确提示"待管理员分配组织与角色"。
- 改密需校验当前密码并过 Django 密码强度校验；成功后当前会话令牌仍有效（不强制登出）。
- 登录记录只返回本人流水（复用 LoginAttempt 审计表），供安全自查。
"""

from django.conf import settings
from rest_framework import status
from rest_framework.parsers import FormParser, MultiPartParser
from rest_framework.permissions import AllowAny, IsAuthenticated
from rest_framework.response import Response
from rest_framework.throttling import ScopedRateThrottle
from rest_framework.views import APIView
from rest_framework_simplejwt.tokens import RefreshToken

from .login_guard import record_attempt
from .models import LoginAttempt
from .password_reset import (
    find_user,
    issue_code,
    mask_target,
    send_reset_code,
    verify_code,
)
from .serializers import (
    ChangePasswordSerializer,
    LoginAttemptSerializer,
    PasswordResetConfirmSerializer,
    RegisterSerializer,
)

# 头像上传限制：≤2MB，常见图片类型
AVATAR_MAX_BYTES = 2 * 1024 * 1024
AVATAR_TYPES = {"image/jpeg", "image/png", "image/webp", "image/gif"}
# 偏好白名单：仅允许这些键自助维护
PREFERENCE_KEYS = {"default_route", "table_density", "page_size", "notify_desktop", "notify_email"}


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


class AuthMethodsView(APIView):
    """登录方式能力探测：账号密码恒开；微信扫码为预留（需配置开放平台后启用）。"""

    permission_classes = [AllowAny]

    def get(self, request):
        return Response(
            {
                "password": True,
                "wechat": {
                    "enabled": bool(getattr(settings, "WECHAT_LOGIN_ENABLED", False)),
                    "note": "微信扫码登录为预留能力，配置微信开放平台/企业微信后启用。",
                },
            }
        )


class PasswordResetRequestView(APIView):
    """找回密码 · 请求验证码。邮箱/手机号/用户名任一定位账号。

    不泄露账号是否存在：无论是否命中都返回 sent=true；命中时附掩码目标，
    便于前端提示。DEBUG 下附 dev_code 方便联调（生产不返回）。
    """

    permission_classes = [AllowAny]
    throttle_classes = [ScopedRateThrottle]
    throttle_scope = "password_reset"

    def post(self, request):
        identifier = (request.data.get("identifier") or "").strip()
        if not identifier:
            return Response({"detail": "请输入邮箱或手机号"}, status=status.HTTP_400_BAD_REQUEST)
        user = find_user(identifier)
        payload = {"sent": True}
        if user is not None:
            target, channel = mask_target(user)
            code = issue_code(identifier)
            send_reset_code(user, code, channel or "log")
            payload["target"] = target
            payload["channel"] = channel
            if settings.DEBUG:
                payload["dev_code"] = code
        return Response(payload)


class PasswordResetConfirmView(APIView):
    """找回密码 · 校验验证码并重设密码。"""

    permission_classes = [AllowAny]
    throttle_classes = [ScopedRateThrottle]
    throttle_scope = "password_reset"

    def post(self, request):
        serializer = PasswordResetConfirmSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        user = find_user(data["identifier"])
        if user is None or not verify_code(data["identifier"], data["code"]):
            return Response({"detail": "验证码无效或已过期"}, status=status.HTTP_400_BAD_REQUEST)
        user.set_password(data["new_password"])
        user.save(update_fields=["password"])
        return Response({"detail": "密码已重置，请用新密码登录"})


class MeAvatarView(APIView):
    """本人头像上传 / 移除。"""

    permission_classes = [IsAuthenticated]
    parser_classes = [MultiPartParser, FormParser]

    def post(self, request):
        file = request.FILES.get("avatar") or request.FILES.get("file")
        if file is None:
            return Response({"detail": "未收到文件"}, status=status.HTTP_400_BAD_REQUEST)
        if file.size > AVATAR_MAX_BYTES:
            return Response({"detail": "图片过大，请控制在 2MB 内"}, status=status.HTTP_400_BAD_REQUEST)
        if getattr(file, "content_type", "") not in AVATAR_TYPES:
            return Response({"detail": "仅支持 JPG / PNG / WEBP / GIF"}, status=status.HTTP_400_BAD_REQUEST)
        user = request.user
        user.avatar = file
        user.save(update_fields=["avatar"])
        return Response({"avatar_url": request.build_absolute_uri(user.avatar.url)})

    def delete(self, request):
        user = request.user
        if user.avatar:
            user.avatar.delete(save=False)
            user.avatar = None
            user.save(update_fields=["avatar"])
        return Response(status=status.HTTP_204_NO_CONTENT)
