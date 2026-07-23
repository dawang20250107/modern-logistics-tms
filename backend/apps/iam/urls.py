from django.urls import path
from rest_framework_simplejwt.views import (
    TokenRefreshView,
    TokenVerifyView,
)

from .account_views import (
    AuthMethodsView,
    ChangePasswordView,
    MeAvatarView,
    MyLoginHistoryView,
    PasswordResetConfirmView,
    PasswordResetRequestView,
    RegisterView,
)
from .auth_views import AuditedTokenObtainPairView
from .views import MeView

urlpatterns = [
    path("token", AuditedTokenObtainPairView.as_view(), name="token_obtain_pair"),
    path("token/refresh", TokenRefreshView.as_view(), name="token_refresh"),
    path("token/verify", TokenVerifyView.as_view(), name="token_verify"),
    path("methods", AuthMethodsView.as_view(), name="auth-methods"),
    path("register", RegisterView.as_view(), name="register"),
    path("password-reset/request", PasswordResetRequestView.as_view(), name="password-reset-request"),
    path("password-reset/confirm", PasswordResetConfirmView.as_view(), name="password-reset-confirm"),
    path("me", MeView.as_view(), name="me"),
    path("me/avatar", MeAvatarView.as_view(), name="me-avatar"),
    path("change-password", ChangePasswordView.as_view(), name="change-password"),
    path("login-history", MyLoginHistoryView.as_view(), name="login-history"),
]
