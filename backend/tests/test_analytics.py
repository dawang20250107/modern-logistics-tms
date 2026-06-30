"""指标中台（指标计算 / 物化 / 查询 API）测试。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.analytics.models import MetricSnapshot
from apps.analytics.registry import compute_metric, list_metrics
from apps.analytics.services import build_dashboard, materialize_daily, metric_trend
from apps.ops.models import Order, Waybill


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def test_registry_lists_metrics_across_domains():
    metrics = list_metrics()
    codes = {m["code"] for m in metrics}
    assert {"ops.waybill_count", "fleet.online_rate", "order.count", "finance.receivable_total"} <= codes
    domains = {m["domain"] for m in metrics}
    assert {"ops", "fleet", "order", "finance"} <= domains


@pytest.mark.django_db
def test_waybill_count_with_dimension():
    Waybill.objects.create(waybill_no="M1", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    Waybill.objects.create(waybill_no="M2", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    Waybill.objects.create(waybill_no="M3", route_name="r", status=Waybill.STATUS_PENDING_DISPATCH)

    result = compute_metric("ops.waybill_count", dimension="status")
    assert result["value"] == 3
    by_status = {b["key"]: b["value"] for b in result["breakdown"]}
    assert by_status["in_transit"] == 2


@pytest.mark.django_db
def test_order_conversion_rate():
    Order.objects.create(order_no="O1", channel=Order.CHANNEL_CS, status=Order.STATUS_CONVERTED)
    Order.objects.create(order_no="O2", channel=Order.CHANNEL_CS, status=Order.STATUS_PENDING_CONFIRM)
    result = compute_metric("order.conversion_rate")
    assert result["value"] == 0.5


@pytest.mark.django_db
def test_dashboard_returns_metric_cards():
    Waybill.objects.create(waybill_no="D1", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    dash = build_dashboard()
    codes = {m["code"] for m in dash["metrics"]}
    assert "ops.in_transit" in codes
    in_transit = next(m for m in dash["metrics"] if m["code"] == "ops.in_transit")
    assert in_transit["value"] == 1


@pytest.mark.django_db
def test_dashboard_populates_breakdown_for_dimensional_metrics():
    """看板默认带出支持维度的指标的首选维度构成（此前 build_dashboard 不传
    dimension，前端 KPI 卡的 breakdown 渲染分支永远是空，属未点亮的能力）。"""
    Waybill.objects.create(waybill_no="DB1", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    Waybill.objects.create(waybill_no="DB2", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    Waybill.objects.create(waybill_no="DB3", route_name="r", status=Waybill.STATUS_PENDING_DISPATCH)

    dash = build_dashboard()
    wb = next(m for m in dash["metrics"] if m["code"] == "ops.waybill_count")
    assert wb["value"] == 3
    # 首选维度为 status（注册声明的第一个维度），构成应被带出
    assert "breakdown" in wb
    by_status = {b["key"]: b["value"] for b in wb["breakdown"]}
    assert by_status["in_transit"] == 2
    assert by_status["pending_dispatch"] == 1

    # 不支持维度的指标不应有 breakdown，且不报错
    in_transit = next(m for m in dash["metrics"] if m["code"] == "ops.in_transit")
    assert "breakdown" not in in_transit


@pytest.mark.django_db
def test_materialize_and_trend():
    Waybill.objects.create(waybill_no="T1", route_name="r")
    count = materialize_daily()
    assert count >= 1
    assert MetricSnapshot.objects.filter(metric_code="ops.waybill_count").exists()
    trend = metric_trend("ops.waybill_count", days=7)
    assert len(trend["series"]) == 1
    assert trend["series"][0]["value"] == 1.0


@pytest.mark.django_db
def test_metric_apis(admin_client):
    Waybill.objects.create(waybill_no="A1", route_name="r", status=Waybill.STATUS_IN_TRANSIT)

    resp = admin_client.get("/api/v1/analytics/metrics?domain=ops")
    assert resp.status_code == 200, resp.content
    assert all(m["domain"] == "ops" for m in resp.json()["data"]["metrics"])

    resp = admin_client.post(
        "/api/v1/analytics/metrics/query", {"codes": ["ops.in_transit", "fleet.online_rate"]}, format="json"
    )
    assert resp.status_code == 200, resp.content
    results = {r["code"]: r["value"] for r in resp.json()["data"]["results"]}
    assert results["ops.in_transit"] == 1

    resp = admin_client.get("/api/v1/analytics/dashboard")
    assert resp.status_code == 200, resp.content
    assert len(resp.json()["data"]["metrics"]) > 0


@pytest.mark.django_db
def test_ai_query_metric_tool():
    from apps.ai.services.tools import execute_tool

    Waybill.objects.create(waybill_no="AIQ1", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    result = execute_tool("analytics.query_metric", {"metric_code": "ops.in_transit"})
    assert result["value"] == 1
    assert "在途" in result["summary"]
