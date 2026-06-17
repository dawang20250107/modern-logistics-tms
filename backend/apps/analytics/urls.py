from django.urls import path

from .views import DashboardView, MetricCatalogView, MetricQueryView, MetricTrendView

urlpatterns = [
    path("metrics", MetricCatalogView.as_view(), name="metric-catalog"),
    path("metrics/query", MetricQueryView.as_view(), name="metric-query"),
    path("metrics/<str:code>/trend", MetricTrendView.as_view(), name="metric-trend"),
    path("dashboard", DashboardView.as_view(), name="analytics-dashboard"),
]
