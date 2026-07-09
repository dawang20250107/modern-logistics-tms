from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError
from apps.iam.permissions import HasPermission

from .registry import compute_metric, list_metrics
from .services import build_dashboard, metric_trend

# 经营看板/指标/数据目录含全公司财务经营口径，须经营分析查看权
PERM_ANALYTICS = "analytics.view"


class _AnalyticsView(APIView):
    permission_classes = [IsAuthenticated, HasPermission]
    required_permissions = PERM_ANALYTICS


class MetricCatalogView(_AnalyticsView):
    """指标目录：列出所有可查询指标定义（口径/主题域/维度）。"""

    def get(self, request):
        return Response({"metrics": list_metrics(request.query_params.get("domain"))})


class MetricQueryView(_AnalyticsView):
    """指标查询：按 codes + 时间范围 + 维度计算（多指标一次取）。"""

    def post(self, request):
        codes = request.data.get("codes") or []
        if not isinstance(codes, list) or not codes:
            raise AppError("CODES_REQUIRED", "codes 必须是非空数组。", status=400)
        start = request.data.get("start")
        end = request.data.get("end")
        dimension = request.data.get("dimension")
        results = [compute_metric(c, start=start, end=end, dimension=dimension) for c in codes]
        return Response({"results": results})


class DashboardView(_AnalyticsView):
    """经营看板：一次返回核心经营/运营指标。"""

    def get(self, request):
        with_trends = (request.query_params.get("trends") or "").lower() in ("1", "true", "yes")
        return Response(build_dashboard(
            request.query_params.get("start"), request.query_params.get("end"), with_trends=with_trends,
        ))


class MetricTrendView(_AnalyticsView):
    """指标趋势：从物化快照取近 N 天序列。"""

    def get(self, request, code):
        days = int(request.query_params.get("days") or 14)
        return Response(metric_trend(code, days))


class DataCatalogView(_AnalyticsView):
    """数据资产目录：业务域 / 表 / 字段 / 记录数（数据治理 lite）。?counts=true"""

    def get(self, request):
        from .catalog import list_data_assets

        with_counts = (request.query_params.get("counts") or "").lower() in ("1", "true", "yes")
        assets = list_data_assets(with_counts=with_counts)
        return Response({
            "assets": assets,
            "total_assets": len(assets),
            "domains": sorted({a["domain"] for a in assets}),
        })
