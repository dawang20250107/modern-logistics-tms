"""运单拆单/合单测试。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.core.exceptions import AppError
from apps.ops.models import Waybill
from apps.ops.services import merge_waybills, split_waybill


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_split_waybill_distributes_cargo_and_voids_parent():
    wb = Waybill.objects.create(
        waybill_no="SP1", route_name="沪-蓉", status=Waybill.STATUS_PENDING_DISPATCH,
        cargo_quantity=100, cargo_weight_ton=20,
    )
    children = split_waybill(wb, [
        {"cargo_quantity": 60, "cargo_weight_ton": 12},
        {"cargo_quantity": 40, "cargo_weight_ton": 8},
    ])
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_VOIDED
    assert len(children) == 2
    assert {c.waybill_no for c in children} == {"SP1-S1", "SP1-S2"}
    assert all(c.parent_id == wb.id for c in children)
    assert sum(c.cargo_quantity for c in children) == 100


@pytest.mark.django_db
def test_split_rejected_when_not_early_status():
    wb = Waybill.objects.create(waybill_no="SP2", route_name="r", status=Waybill.STATUS_IN_TRANSIT)
    with pytest.raises(AppError):
        split_waybill(wb, [{"cargo_quantity": 1}, {"cargo_quantity": 1}])


@pytest.mark.django_db
def test_merge_waybills_sums_cargo_and_voids_sources():
    a = Waybill.objects.create(waybill_no="MG1", route_name="沪-蓉", cargo_quantity=10, cargo_weight_ton=5)
    b = Waybill.objects.create(waybill_no="MG2", route_name="沪-蓉", cargo_quantity=20, cargo_weight_ton=7)
    merged = merge_waybills([a, b])
    a.refresh_from_db()
    b.refresh_from_db()
    assert merged.waybill_no == "MG1-M"
    assert merged.cargo_quantity == 30
    assert float(merged.cargo_weight_ton) == 12
    assert a.status == Waybill.STATUS_VOIDED and a.parent_id == merged.id
    assert b.status == Waybill.STATUS_VOIDED and b.parent_id == merged.id


@pytest.mark.django_db
def test_split_endpoint(admin_client):
    Waybill.objects.create(
        waybill_no="SPAPI", route_name="沪-蓉", status=Waybill.STATUS_PENDING_DISPATCH, cargo_quantity=50
    )
    resp = admin_client.post(
        "/api/v1/waybills/SPAPI/split",
        {"splits": [{"cargo_quantity": 30}, {"cargo_quantity": 20}]},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert len(resp.json()["data"]["children"]) == 2


@pytest.mark.django_db
def test_merge_endpoint(admin_client):
    Waybill.objects.create(waybill_no="MGA", route_name="沪-蓉", cargo_quantity=10)
    Waybill.objects.create(waybill_no="MGB", route_name="沪-蓉", cargo_quantity=15)
    resp = admin_client.post(
        "/api/v1/waybills/merge", {"waybill_nos": ["MGA", "MGB"]}, format="json"
    )
    assert resp.status_code == 201, resp.content
    assert resp.json()["data"]["cargo"]["quantity"] == 25
