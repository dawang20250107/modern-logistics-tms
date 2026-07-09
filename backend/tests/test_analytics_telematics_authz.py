"""经营看板 / 车联网端点鉴权收口：读需查看权、写需管理权，超管畅通。"""

import pytest
from django.core.cache import cache
from rest_framework.test import APIClient

from apps.iam.models import Permission, Role, RoleAssignment


@pytest.fixture(autouse=True)
def _clear_cache():
    cache.clear()
    yield
    cache.clear()


def _user(username, perms=()):
    from django.contrib.auth import get_user_model

    u = get_user_model().objects.create_user(username=username, password="x")
    if perms:
        role = Role.objects.create(code=f"r_{username}", name=username, data_scope="all")
        role.permissions.set(
            [Permission.objects.get_or_create(code=c, defaults={"name": c})[0] for c in perms]
        )
        RoleAssignment.objects.create(user=u, role=role)
    return u


def _client(user):
    c = APIClient()
    c.force_authenticate(user=user)
    return c


def _su():
    from django.contrib.auth import get_user_model

    return get_user_model().objects.create_superuser(username="root", password="x")


@pytest.mark.django_db
def test_analytics_requires_permission():
    c = _client(_user("plain"))
    assert c.get("/api/v1/analytics/metrics").status_code == 403
    assert c.get("/api/v1/analytics/dashboard").status_code == 403
    assert c.get("/api/v1/analytics/catalog").status_code == 403


@pytest.mark.django_db
def test_analytics_holder_and_superuser_ok():
    assert _client(_user("viewer", ["analytics.view"])).get("/api/v1/analytics/metrics").status_code == 200
    assert _client(_su()).get("/api/v1/analytics/dashboard").status_code == 200


@pytest.mark.django_db
def test_telematics_read_requires_view_permission():
    c = _client(_user("plain2"))
    assert c.get("/api/v1/telematics/vehicles/live").status_code == 403
    assert c.get("/api/v1/telematics/command-center/summary").status_code == 403
    assert c.get("/api/v1/telematics/devices").status_code == 403
    assert c.get("/api/v1/telematics/alerts").status_code == 403


@pytest.mark.django_db
def test_telematics_view_holder_can_read_but_not_write():
    c = _client(_user("t_viewer", ["telematics.view"]))
    assert c.get("/api/v1/telematics/vehicles/live").status_code == 200
    assert c.get("/api/v1/telematics/devices").status_code == 200
    # 无 telematics.manage → 建设备/围栏 403
    assert c.post("/api/v1/telematics/devices", {"device_no": "D1"}, format="json").status_code == 403
    assert c.post(
        "/api/v1/telematics/geofences", {"name": "G1", "shape": "circle"}, format="json"
    ).status_code == 403


@pytest.mark.django_db
def test_telematics_manage_holder_can_write():
    c = _client(_user("t_mgr", ["telematics.view", "telematics.manage"]))
    r = c.post("/api/v1/telematics/devices", {"device_no": "D-NEW", "device_type": "obd"}, format="json")
    assert r.status_code in (201, 400)  # 鉴权通过（201 或字段校验 400，但非 403）
    assert r.status_code != 403
