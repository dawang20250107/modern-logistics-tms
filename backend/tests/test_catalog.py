"""数据资产目录。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.analytics.catalog import list_data_assets


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def test_list_data_assets_covers_domains():
    assets = list_data_assets()
    apps_seen = {a["app"] for a in assets}
    assert {"ops", "finance", "telematics", "masterdata"} <= apps_seen
    waybill = next(a for a in assets if a["model"] == "Waybill")
    assert waybill["table"] == "ops_waybill"
    assert waybill["field_count"] > 5


@pytest.mark.django_db
def test_catalog_endpoint_with_counts(admin_client):
    resp = admin_client.get("/api/v1/analytics/catalog?counts=true")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["total_assets"] > 10
    assert all("row_count" in a for a in data["assets"])
    assert "运单/订单" in data["domains"]
