"""主数据端点鉴权：客户/车辆/司机/线路/伙伴/证件读写受 masterdata.* 权限约束。"""

import pytest
from django.core.cache import cache
from rest_framework.test import APIClient

from apps.iam.models import Permission, Role, RoleAssignment
from apps.masterdata.models import Customer


@pytest.fixture(autouse=True)
def _clear_perm_cache():
    cache.clear()
    yield
    cache.clear()


@pytest.fixture
def users(db):
    from django.contrib.auth import get_user_model

    User = get_user_model()
    return (
        User.objects.create_superuser(username="root", password="pw-strong-123456"),
        User.objects.create_user(username="nobody", password="pw-strong-123456"),
        User.objects.create_user(username="viewer", password="pw-strong-123456"),
        User.objects.create_user(username="manager", password="pw-strong-123456"),
    )


def _grant(user, *codes):
    role = Role.objects.create(code=f"r_{user.username}", name=user.username, data_scope="all")
    perms = [Permission.objects.get_or_create(code=c, defaults={"name": c, "module": "主数据"})[0] for c in codes]
    role.permissions.set(perms)
    RoleAssignment.objects.create(user=user, role=role)


def _auth(user):
    c = APIClient()
    c.force_authenticate(user=user)
    return c


@pytest.mark.django_db
def test_masterdata_endpoints_require_permission(users):
    root, plain, viewer, manager = users
    _grant(viewer, "masterdata.view")
    _grant(manager, "masterdata.view", "masterdata.manage")

    # 无权限：读写皆 403（此前主数据仅需登录即可，属越权面）
    cp = _auth(plain)
    for url in ["/api/v1/customers", "/api/v1/vehicles", "/api/v1/drivers", "/api/v1/routes",
                "/api/v1/b2b-partners", "/api/v1/credentials/expiring"]:
        assert cp.get(url).status_code == 403, url
    assert cp.post("/api/v1/customers", {"code": "X", "name": "越权建"}, format="json").status_code == 403

    # 仅 masterdata.view：可读，不可写
    cv = _auth(viewer)
    assert cv.get("/api/v1/customers").status_code == 200
    assert cv.get("/api/v1/credentials/expiring").status_code == 200
    assert cv.post("/api/v1/customers", {"code": "Y", "name": "只读越权"}, format="json").status_code == 403

    # masterdata.manage：可写
    cm = _auth(manager)
    r = cm.post("/api/v1/customers", {"code": "Z1", "name": "新客户"}, format="json")
    assert r.status_code == 201, r.content
    assert Customer.objects.filter(code="Z1").exists()

    # 超管：畅通
    assert _auth(root).get("/api/v1/vehicles").status_code == 200


@pytest.mark.django_db
def test_driver_lookup_is_read_gated(users):
    _root, plain, viewer, _m = users
    _grant(viewer, "masterdata.view")
    # 自定义只读动作 lookup 按 read 归类：无权 403，有 view 放行
    assert _auth(plain).get("/api/v1/drivers/lookup?name=x&id_tail=123456").status_code == 403
    assert _auth(viewer).get("/api/v1/drivers/lookup?name=x&id_tail=123456").status_code == 200
