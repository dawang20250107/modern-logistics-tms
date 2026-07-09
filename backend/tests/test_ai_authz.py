"""AI/Agent 端点鉴权与数据范围收口：需 ai.use 权限，查单按组织范围过滤。"""

import pytest
from django.core.cache import cache
from rest_framework.test import APIClient

from apps.iam.models import Organization, Permission, Role, RoleAssignment
from apps.ops.models import Waybill


@pytest.fixture(autouse=True)
def _clear_cache():
    cache.clear()
    yield
    cache.clear()


def _user(username, org=None, perms=()):
    from django.contrib.auth import get_user_model

    u = get_user_model().objects.create_user(username=username, password="x", organization=org)
    if perms:
        role = Role.objects.create(code=f"r_{username}", name=username, data_scope="org_sub")
        role.permissions.set(
            [Permission.objects.get_or_create(code=c, defaults={"name": c})[0] for c in perms]
        )
        RoleAssignment.objects.create(user=u, role=role, organization=org)
    return u


def _client(user):
    c = APIClient()
    c.force_authenticate(user=user)
    return c


@pytest.mark.django_db
def test_query_waybill_requires_ai_permission():
    user = _user("nobody")  # 无 ai.use
    r = _client(user).post("/api/v1/ai/query-waybill", {"query": ""}, format="json")
    assert r.status_code == 403


@pytest.mark.django_db
def test_agent_endpoints_require_ai_permission():
    user = _user("nobody2")
    c = _client(user)
    assert c.post("/api/v1/agent/chat", {"message": "hi"}, format="json").status_code == 403
    assert c.post("/api/v1/agent/tools/execute", {"tool_name": "x"}, format="json").status_code == 403
    assert c.get("/api/v1/agent/tools").status_code == 403


@pytest.mark.django_db
def test_query_waybill_scoped_to_org():
    hq = Organization.objects.create(code="HQ", name="总部", type="group")
    c1 = Organization.objects.create(code="C1", name="一部", type="dept", parent=hq)
    c2 = Organization.objects.create(code="C2", name="二部", type="dept", parent=hq)
    Waybill.objects.create(waybill_no="WB-C1", route_name="r", organization=c1)
    Waybill.objects.create(waybill_no="WB-C2", route_name="r", organization=c2)

    user = _user("c1user", org=c1, perms=["ai.use"])
    r = _client(user).post("/api/v1/ai/query-waybill", {"query": "WB"}, format="json")
    assert r.status_code == 200
    nos = {w["waybill_no"] for w in r.json()["data"]["waybills"]}
    assert nos == {"WB-C1"}  # 看不到 C2 的运单


@pytest.mark.django_db
def test_superuser_sees_all_in_query():
    from django.contrib.auth import get_user_model

    hq = Organization.objects.create(code="HQ", name="总部", type="group")
    Waybill.objects.create(waybill_no="WB-A", route_name="r", organization=hq)
    Waybill.objects.create(waybill_no="WB-B", route_name="r", organization=None)
    su = get_user_model().objects.create_superuser(username="root", password="x")
    r = _client(su).post("/api/v1/ai/query-waybill", {"query": "WB"}, format="json")
    assert {w["waybill_no"] for w in r.json()["data"]["waybills"]} == {"WB-A", "WB-B"}


@pytest.mark.django_db
def test_ai_holder_can_query():
    hq = Organization.objects.create(code="HQ", name="总部", type="group")
    Waybill.objects.create(waybill_no="WB-Q", route_name="r", organization=hq)
    user = _user("aiuser", org=hq, perms=["ai.use"])
    r = _client(user).post("/api/v1/ai/query-waybill", {"query": ""}, format="json")
    assert r.status_code == 200
