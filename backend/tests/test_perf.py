"""性能：列表接口消除 N+1（查询数不随行数线性增长）+ 慢请求观测。"""

import pytest
from django.db import connection
from django.test.utils import CaptureQueriesContext
from rest_framework.test import APIClient

from apps.masterdata.models import Driver, Vehicle
from apps.ops.models import Waybill, WaybillDriver


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="perf_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "perf_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


def _make_waybills(count, start=0):
    veh, _ = Vehicle.objects.get_or_create(
        plate_no="沪P0001", defaults={"load_capacity_ton": 30}
    )
    for i in range(start, start + count):
        d = Driver.objects.create(name=f"司机{i}", phone=f"139{i:08d}")
        wb = Waybill.objects.create(waybill_no=f"PERF{i:03d}", route_name="r", vehicle=veh, driver=d)
        WaybillDriver.objects.create(waybill=wb, driver=d, role="main")


@pytest.mark.django_db
def test_waybill_list_no_n_plus_one(admin_client):
    # 5 单与 15 单的查询次数应基本一致（已 prefetch driver_assignments）
    _make_waybills(5)
    with CaptureQueriesContext(connection) as q5:
        r = admin_client.get("/api/v1/waybills?page_size=50")
    assert r.status_code == 200
    n5 = len(q5)

    _make_waybills(10, start=5)  # 共 15 单
    with CaptureQueriesContext(connection) as q15:
        r = admin_client.get("/api/v1/waybills?page_size=50")
    assert r.status_code == 200
    n15 = len(q15)

    # 不随行数线性增长（容许少量差异）
    assert n15 <= n5 + 3, f"疑似 N+1：5单 {n5} 次，15单 {n15} 次"


@pytest.mark.django_db
def test_customer_list_skips_heavy_history(admin_client):
    from apps.masterdata.models import Customer

    for i in range(8):
        Customer.objects.create(code=f"P{i}", name=f"客户{i}")
    with CaptureQueriesContext(connection) as q:
        r = admin_client.get("/api/v1/customers?page_size=50")
    assert r.status_code == 200
    # 列表不逐行聚合历史（history 为 None）
    assert all(item["history"] is None for item in r.json()["data"]["items"])
    assert len(q) < 12
