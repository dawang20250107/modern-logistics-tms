"""RBAC 端点鉴权：无权限的普通登录用户不得触达组织/角色/员工写操作。

修补此前的越权漏洞——RBAC 引擎已建但仅 WaybillViewSet 强制，任何已认证用户
都能自授角色、改组织树。此处验证：无权限用户 403，持相应权限点者放行，超管畅通。
"""

import pytest
from django.core.cache import cache
from rest_framework.test import APIClient

from apps.iam.models import (
    Employee,
    Organization,
    Permission,
    Role,
    RoleAssignment,
)


def _client_for(user):
    c = APIClient()
    c.force_authenticate(user=user)
    return c


@pytest.fixture(autouse=True)
def _clear_perm_cache():
    # 权限点带 60s Redis 短缓存，逐用例清空避免跨用例串味
    cache.clear()
    yield
    cache.clear()


@pytest.fixture
def users(db):
    from django.contrib.auth import get_user_model

    User = get_user_model()
    superuser = User.objects.create_superuser(username="root", password="pw-strong-123456")
    plain = User.objects.create_user(username="nobody", password="pw-strong-123456")
    return superuser, plain


@pytest.fixture
def rbac_role(db):
    """一个仅含 org.rbac 权限点的角色。"""
    role = Role.objects.create(code="rbac_admin", name="权限管理员", data_scope="org")
    perm = Permission.objects.create(code="org.rbac", name="角色权限管理", module="组织")
    role.permissions.set([perm])
    return role


@pytest.mark.django_db
def test_plain_user_cannot_manage_roles(users):
    _, plain = users
    c = _client_for(plain)
    # 建角色（写）→ 403
    r = c.post("/api/v1/org/roles", {"code": "x", "name": "x", "data_scope": "org"}, format="json")
    assert r.status_code == 403
    # 列角色（读，含权限点信息）→ 403
    assert c.get("/api/v1/org/roles").status_code == 403
    # 权限点清单 → 403
    assert c.get("/api/v1/org/permissions").status_code == 403
    # 角色×权限矩阵 → 403
    assert c.get("/api/v1/org/rbac/matrix").status_code == 403


@pytest.mark.django_db
def test_plain_user_cannot_write_org_or_employees(users):
    _, plain = users
    c = _client_for(plain)
    # 改组织树 → 403
    r = c.post("/api/v1/org/organizations", {"code": "H", "name": "黑客总部", "type": "group"}, format="json")
    assert r.status_code == 403
    # 建员工 → 403
    r2 = c.post("/api/v1/org/employees", {"employee_no": "Z9", "name": "越权"}, format="json")
    assert r2.status_code == 403


@pytest.mark.django_db
def test_plain_user_cannot_self_grant_role(users, rbac_role):
    """核心越权场景：普通用户不能给自己的账号挂角色。"""
    _, plain = users
    org = Organization.objects.create(code="SH", name="上海", type="station")
    emp = Employee.objects.create(employee_no="P1", name="本人", organization=org, user=plain)
    c = _client_for(plain)
    r = c.post(
        f"/api/v1/org/employees/{emp.id}/roles",
        {"roles": [str(rbac_role.id)]}, format="json",
    )
    assert r.status_code == 403
    assert not RoleAssignment.objects.filter(user=plain).exists()


@pytest.mark.django_db
def test_rbac_permission_holder_can_manage_roles(users, rbac_role):
    """持 org.rbac 的用户可读写角色/权限矩阵。"""
    _, plain = users
    RoleAssignment.objects.create(user=plain, role=rbac_role)
    c = _client_for(plain)
    assert c.get("/api/v1/org/roles").status_code == 200
    assert c.get("/api/v1/org/permissions").status_code == 200
    assert c.get("/api/v1/org/rbac/matrix").status_code == 200
    r = c.post(
        "/api/v1/org/roles",
        {"code": "newrole", "name": "新角色", "data_scope": "org"}, format="json",
    )
    assert r.status_code == 201


@pytest.mark.django_db
def test_org_view_holder_reads_but_cannot_write(users, db):
    """持 org.view 的用户可读组织，但不能改组织树，也进不了角色管理。"""
    role = Role.objects.create(code="viewer", name="组织查看", data_scope="org")
    perm = Permission.objects.create(code="org.view", name="组织查看", module="组织")
    role.permissions.set([perm])
    _, plain = users
    RoleAssignment.objects.create(user=plain, role=role)
    c = _client_for(plain)
    assert c.get("/api/v1/org/organizations").status_code == 200
    assert c.get("/api/v1/org/overview").status_code == 200
    # 无 org.manage → 写组织 403
    assert c.post(
        "/api/v1/org/organizations", {"code": "N", "name": "N", "type": "group"}, format="json"
    ).status_code == 403
    # 无 org.rbac → 角色管理 403
    assert c.get("/api/v1/org/roles").status_code == 403


@pytest.mark.django_db
def test_superuser_bypasses_all(users):
    superuser, _ = users
    c = _client_for(superuser)
    assert c.get("/api/v1/org/roles").status_code == 200
    assert c.get("/api/v1/org/rbac/matrix").status_code == 200
    r = c.post(
        "/api/v1/org/organizations", {"code": "SU", "name": "超管建的", "type": "group"}, format="json"
    )
    assert r.status_code == 201


@pytest.mark.django_db
def test_me_returns_permission_codes(users, rbac_role):
    _, plain = users
    RoleAssignment.objects.create(user=plain, role=rbac_role)
    c = _client_for(plain)
    data = c.get("/api/v1/auth/me").json()["data"]
    assert data["permissions"] == ["org.rbac"]


@pytest.mark.django_db
def test_me_returns_wildcard_for_superuser(users):
    superuser, _ = users
    c = _client_for(superuser)
    data = c.get("/api/v1/auth/me").json()["data"]
    assert data["permissions"] == ["*"]
