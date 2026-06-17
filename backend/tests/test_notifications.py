"""通知中心：扇出、未读计数、已读。"""

import pytest
from django.contrib.auth import get_user_model
from rest_framework.test import APIClient

from apps.iam.models import Organization, Role, RoleAssignment
from apps.notifications.models import Notification
from apps.notifications.services import notify_role, notify_user
from apps.ops.intake import create_order_from_intake, pool_order
from apps.ops.models import Order

User = get_user_model()


def _client(user):
    client = APIClient()
    client.force_authenticate(user)
    return client


@pytest.mark.django_db
def test_notify_user_and_unread_count():
    u = User.objects.create_user(username="cs1", password="x")
    notify_user(u, category="test", title="hi")
    notify_user(u, category="test", title="hi2")
    client = _client(u)
    resp = client.get("/api/v1/notifications/unread-count")
    assert resp.json()["data"]["unread"] == 2


@pytest.mark.django_db
def test_read_and_read_all():
    u = User.objects.create_user(username="cs2", password="x")
    notify_user(u, category="t", title="a")
    notify_user(u, category="t", title="b")
    client = _client(u)
    nid = client.get("/api/v1/notifications").json()["data"]["items"][0]["id"]
    client.post(f"/api/v1/notifications/{nid}/read")
    assert Notification.objects.get(id=nid).is_read is True
    client.post("/api/v1/notifications/read-all")
    assert Notification.objects.filter(recipient=u, is_read=False).count() == 0


@pytest.mark.django_db
def test_pool_notifies_dispatchers():
    org = Organization.objects.create(name="网点", code="ST1")
    role = Role.objects.create(code="dispatcher", name="调度员")
    disp = User.objects.create_user(username="disp", password="x")
    RoleAssignment.objects.create(user=disp, role=role, organization=org)

    order = create_order_from_intake(fields={"origin": "上海", "destination": "成都"})
    order.status = Order.STATUS_CONFIRMED
    order.save()
    pool_order(order)

    assert Notification.objects.filter(recipient=disp, category="order_pooled").exists()


@pytest.mark.django_db
def test_notify_role_no_users_is_safe():
    assert notify_role("nonexistent_role", category="x", title="y") == 0
