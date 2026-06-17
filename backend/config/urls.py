from django.conf import settings
from django.conf.urls.static import static
from django.contrib import admin
from django.urls import include, path
from drf_spectacular.views import SpectacularAPIView, SpectacularSwaggerView

from apps.core.sse import event_stream

urlpatterns = [
    path("admin/", admin.site.urls),
    # 健康/就绪探针（纯 Django，无鉴权，供编排与负载均衡使用）
    path("", include("apps.core.urls")),
    # Prometheus 指标 /metrics
    path("", include("django_prometheus.urls")),
    # OpenAPI 契约与 Swagger UI
    path("api/schema", SpectacularAPIView.as_view(), name="schema"),
    path("api/docs", SpectacularSwaggerView.as_view(url_name="schema"), name="docs"),
    # 业务 API v1
    path("api/v1/auth/", include("apps.iam.urls")),
    path("api/v1/", include("apps.masterdata.urls")),
    path("api/v1/", include("apps.ops.urls")),
    path("api/v1/finance/", include("apps.finance.urls")),
    path("api/v1/", include("apps.ai.urls")),
    # 实时事件流（SSE）
    path("api/v1/stream/events", event_stream, name="event-stream"),
]

if settings.DEBUG:
    urlpatterns += static(settings.MEDIA_URL, document_root=settings.MEDIA_ROOT)
