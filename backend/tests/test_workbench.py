"""个人工作台聚合。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.ops.intake import create_order_from_intake, pool_order
from apps.ops.models import Order

User = get_user_model()


@pytest.mark.django_db
def test_workbench_aggregates_by_role():
    cs = User.objects.create_user(username="cs", password="x")
    client = APIClient()
    client.force_authenticate(cs)

    # cs 建两单（待确认）
    create_order_from_intake(fields={"origin": "A", "destination": "B"}, operator=cs)
    create_order_from_intake(fields={"origin": "C", "destination": "D"}, operator=cs)
    # 一单进池
    o = create_order_from_intake(fields={"origin": "E", "destination": "F"})
    o.status = Order.STATUS_CONFIRMED
    o.save()
    pool_order(o)

    resp = client.get("/api/v1/workbench")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["cs"]["my_orders_pending_confirm"] == 2
    assert data["cs"]["my_orders_today"] == 2
    assert data["dispatch"]["pool_count"] == 1
    assert "unread_notifications" in data["common"]
    assert len(data["cs"]["recent_pending"]) == 2
