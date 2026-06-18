"""车队合规预警：证件到期 days_left / severity / 排序。"""

from datetime import timedelta

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.masterdata.models import Driver, Vehicle


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


@pytest.mark.django_db
def test_expiring_credentials_days_left_and_sort(admin_client):
    today = timezone.localdate()
    Vehicle.objects.create(plate_no="沪A00001", insurance_expiry=today - timedelta(days=3))  # 已过期
    Vehicle.objects.create(plate_no="沪A00002", inspection_expiry=today + timedelta(days=5))  # critical
    Driver.objects.create(name="张师傅", license_expiry=today + timedelta(days=20))  # warning
    Driver.objects.create(name="李师傅", qualification_expiry=today + timedelta(days=100))  # 超窗口，不计

    resp = admin_client.get("/api/v1/credentials/expiring?days=30")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]

    assert data["summary"]["total"] == 3
    assert data["summary"]["expired"] == 1
    assert data["summary"]["critical"] == 1
    assert data["summary"]["warning"] == 1

    # 车辆按 days_left 升序：已过期(-3) 在最前
    plates = [r["plate_no"] for r in data["vehicles"]]
    assert plates == ["沪A00001", "沪A00002"]
    assert data["vehicles"][0]["days_left"] == -3
    assert data["vehicles"][0]["severity"] == "expired"

    drivers = data["drivers"]
    assert len(drivers) == 1
    assert drivers[0]["name"] == "张师傅"
    assert drivers[0]["severity"] == "warning"
