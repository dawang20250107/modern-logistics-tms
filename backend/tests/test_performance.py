"""性能回归：核心列表无 N+1（订单数增加，查询数恒定）。"""

import pytest
from django.contrib.auth import get_user_model
from django.db import connection
from django.test.utils import CaptureQueriesContext
from rest_framework.test import APIClient

from apps.masterdata.models import Customer
from apps.ops.intake import create_order_from_intake

User = get_user_model()


@pytest.fixture
def admin_client():
    User.objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def _make_orders(n, creator):
    import uuid

    cust = Customer.objects.create(code=f"C{uuid.uuid4().hex[:8]}", name="客户")
    for _ in range(n):
        create_order_from_intake(fields={"origin": "上海", "destination": "成都"}, customer=cust, operator=creator)


def _query_count(client, url):
    with CaptureQueriesContext(connection) as ctx:
        resp = client.get(url)
    assert resp.status_code == 200
    return len(ctx)


@pytest.mark.django_db
def test_order_list_no_n_plus_one(admin_client):
    creator = User.objects.get(username="a")
    _make_orders(3, creator)
    c_small = _query_count(admin_client, "/api/v1/orders?page_size=100")
    _make_orders(7, creator)
    c_large = _query_count(admin_client, "/api/v1/orders?page_size=100")
    # 订单从 3 增到 10，查询数应恒定（select_related 生效，无逐行查询）
    assert c_small == c_large


@pytest.mark.django_db
def test_workbench_constant_queries(admin_client):
    creator = User.objects.get(username="a")
    _make_orders(2, creator)
    c_small = _query_count(admin_client, "/api/v1/workbench")
    _make_orders(8, creator)
    c_large = _query_count(admin_client, "/api/v1/workbench")
    assert c_small == c_large
