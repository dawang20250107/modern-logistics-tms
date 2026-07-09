from django.urls import path
from rest_framework_simplejwt.views import (
    TokenRefreshView,
    TokenVerifyView,
)

from .auth_views import AuditedTokenObtainPairView
from .views import MeView

urlpatterns = [
    path("token", AuditedTokenObtainPairView.as_view(), name="token_obtain_pair"),
    path("token/refresh", TokenRefreshView.as_view(), name="token_refresh"),
    path("token/verify", TokenVerifyView.as_view(), name="token_verify"),
    path("me", MeView.as_view(), name="me"),
]
