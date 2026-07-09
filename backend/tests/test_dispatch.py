"""智能调度/排线测试。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.finance.models import PricingRule
from apps.masterdata.models import Carrier, Vehicle
from apps.ops.dispatch import carrier_quotes, plan_dispatch, rank_vehicles, recommend_dispatch, vehicle_fit
from apps.ops.models import Waybill


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_vehicle_fit_respects_capacity():
    big = Vehicle.objects.create(plate_no="大车", load_capacity_ton=30)
    small = Vehicle.objects.create(plate_no="小车", load_capacity_ton=5)
    wb = Waybill.objects.create(waybill_no="D1", route_name="r", cargo_weight_ton=10)
    assert vehicle_fit(big, wb) is not None
    assert vehicle_fit(small, wb) is None  # 超核载


@pytest.mark.django_db
def test_rank_vehicles_prefers_tight_fit():
    Vehicle.objects.create(plate_no="车30", load_capacity_ton=30)
    Vehicle.objects.create(plate_no="车12", load_capacity_ton=12)
    wb = Waybill.objects.create(waybill_no="D2", route_name="r", cargo_weight_ton=10)
    ranked = rank_vehicles(wb)
    assert ranked[0]["plate_no"] == "车12"  # 余量更小、装载更紧凑


@pytest.mark.django_db
def test_carrier_quotes_sorted_by_price():
    c1 = Carrier.objects.create(code="C1", name="便宜承运")
    c2 = Carrier.objects.create(code="C2", name="贵承运")
    PricingRule.objects.create(name="r1", price_type=PricingRule.PRICE_TYPE_COST, expense_item_code="FREIGHT", carrier=c1, base_price=1000)
    PricingRule.objects.create(name="r2", price_type=PricingRule.PRICE_TYPE_COST, expense_item_code="FREIGHT", carrier=c2, base_price=2000)
    wb = Waybill.objects.create(waybill_no="D3", route_name="r", cargo_weight_ton=10)
    quotes = carrier_quotes(wb)
    assert [q["carrier"] for q in quotes] == ["便宜承运", "贵承运"]


@pytest.mark.django_db
def test_plan_dispatch_greedy_assigns():
    Vehicle.objects.create(plate_no="A", load_capacity_ton=20)
    Vehicle.objects.create(plate_no="B", load_capacity_ton=20)
    wbs = [
        Waybill.objects.create(waybill_no="P1", route_name="r", cargo_weight_ton=15, status=Waybill.STATUS_PENDING_DISPATCH),
        Waybill.objects.create(waybill_no="P2", route_name="r", cargo_weight_ton=8, status=Waybill.STATUS_PENDING_DISPATCH),
        Waybill.objects.create(waybill_no="P3", route_name="r", cargo_weight_ton=5, status=Waybill.STATUS_PENDING_DISPATCH),
    ]
    plan = plan_dispatch(wbs)
    assert plan["assigned_count"] == 2  # 仅 2 辆车
    assert "P3" in plan["unassigned"]


@pytest.mark.django_db
def test_dispatch_recommendation_endpoint(admin_client):
    Vehicle.objects.create(plate_no="推荐车", load_capacity_ton=20)
    Waybill.objects.create(waybill_no="REC1", route_name="r", cargo_weight_ton=10, status=Waybill.STATUS_PENDING_DISPATCH)
    resp = admin_client.get("/api/v1/waybills/REC1/dispatch-recommendation")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["best_vehicle"]["plate_no"] == "推荐车"


@pytest.mark.django_db
def test_recommend_dispatch_structure():
    wb = Waybill.objects.create(waybill_no="REC2", route_name="r", cargo_weight_ton=10)
    result = recommend_dispatch(wb)
    assert set(result) >= {"vehicle_candidates", "driver_candidates", "carrier_quotes", "best_vehicle"}


@pytest.mark.django_db
def test_rank_vehicles_deprioritizes_expired_credentials():
    from datetime import timedelta

    from django.utils import timezone

    today = timezone.localdate()
    # 证件过期车 P0-3 后被硬阻断，默认不进推荐；合规车正常入选
    expired = Vehicle.objects.create(plate_no="过期车", load_capacity_ton=12, insurance_expiry=today - timedelta(days=2))
    Vehicle.objects.create(plate_no="合规车", load_capacity_ton=20)
    wb = Waybill.objects.create(waybill_no="CMP1", route_name="r", cargo_weight_ton=10)
    ranked = rank_vehicles(wb)
    assert [r["plate_no"] for r in ranked] == ["合规车"]  # 过期车被硬阻断
    assert ranked[0]["compliance_ok"] is True
    # include_blocked=True 时仍可见并带屏蔽标记
    with_blocked = rank_vehicles(wb, include_blocked=True)
    expired_row = next(r for r in with_blocked if r["plate_no"] == "过期车")
    assert "保险" in expired_row["compliance"]
    assert expired_row["blocked"] is True
    assert vehicle_fit(expired, wb)["compliance"] == ["保险"]
