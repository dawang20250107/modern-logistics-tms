"""组织中台：组织树/人头汇总、员工生命周期、账号移交、总览看板。"""

import pytest
from rest_framework.test import APIClient

from apps.iam.models import Department, Employee, Organization, ServiceArea


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="org_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "org_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


@pytest.fixture
def org_tree(db):
    group = Organization.objects.create(code="JT", name="集团", type="group")
    east = Organization.objects.create(code="EAST", name="华东公司", type="company", parent=group)
    sh = Organization.objects.create(code="SH", name="上海网点", type="station", parent=east, org_property="self")
    return group, east, sh


def _emp(no, name, org, **kw):
    return Employee.objects.create(employee_no=no, name=name, organization=org, **kw)


@pytest.mark.django_db
def test_org_tree_rolls_up_headcount(admin_client, org_tree):
    group, east, sh = org_tree
    _emp("E1", "张三", sh)
    _emp("E2", "李四", sh)
    _emp("E3", "王五", east)

    r = admin_client.get("/api/v1/org/organizations/tree")
    assert r.status_code == 200
    tree = r.json()["data"]["tree"]
    # 单根：集团
    assert len(tree) == 1
    grp = tree[0]
    assert grp["code"] == "JT"
    # 集团子树合计 3 人，自身直属 0
    assert grp["direct_headcount"] == 0
    assert grp["total_headcount"] == 3
    east_node = grp["children"][0]
    assert east_node["code"] == "EAST"
    assert east_node["direct_headcount"] == 1
    assert east_node["total_headcount"] == 3  # 自身 1 + 上海 2
    sh_node = east_node["children"][0]
    assert sh_node["total_headcount"] == 2


@pytest.mark.django_db
def test_employee_lifecycle(admin_client, org_tree):
    from django.contrib.auth import get_user_model

    _, _, sh = org_tree
    user = get_user_model().objects.create_user(username="driver_zhang", password="x")
    emp = _emp("E10", "张师傅", sh, user=user)

    # 停用 → 账号禁登 + 状态 disabled
    r = admin_client.post(f"/api/v1/org/employees/{emp.id}/disable")
    assert r.status_code == 200
    assert r.json()["data"]["status"] == "disabled"
    user.refresh_from_db()
    assert user.is_active is False

    # 启用 → 恢复
    r = admin_client.post(f"/api/v1/org/employees/{emp.id}/enable")
    assert r.status_code == 200
    user.refresh_from_db()
    assert user.is_active is True

    # 重置密码 → 返回新口令且实际生效
    r = admin_client.post(f"/api/v1/org/employees/{emp.id}/reset-password")
    assert r.status_code == 200
    new_pwd = r.json()["data"]["password"]
    user.refresh_from_db()
    assert user.check_password(new_pwd)


@pytest.mark.django_db
def test_reset_password_requires_account(admin_client, org_tree):
    _, _, sh = org_tree
    emp = _emp("E11", "无账号员工", sh)
    r = admin_client.post(f"/api/v1/org/employees/{emp.id}/reset-password")
    assert r.status_code == 400


@pytest.mark.django_db
def test_account_handover(admin_client, org_tree):
    from django.contrib.auth import get_user_model

    _, _, sh = org_tree
    boss_user = get_user_model().objects.create_user(username="boss_wang", password="x")
    boss = _emp("M1", "老王", sh, user=boss_user)
    successor = _emp("M2", "小李", sh)
    sub1 = _emp("S1", "下属甲", sh, supervisor=boss)
    sub2 = _emp("S2", "下属乙", sh, supervisor=boss)
    dept = Department.objects.create(organization=sh, code="OPS", name="运营部", manager=boss)

    r = admin_client.post(
        f"/api/v1/org/employees/{boss.id}/handover",
        {"to_employee": str(successor.id), "reason": "离职交接"},
        format="json",
    )
    assert r.status_code == 201
    body = r.json()["data"]
    assert body["moved_reports"] == 2
    assert body["moved_departments"] == 1
    assert body["disabled_account"] is True

    for obj in (sub1, sub2, dept, boss):
        obj.refresh_from_db()
    assert sub1.supervisor_id == successor.id
    assert sub2.supervisor_id == successor.id
    assert dept.manager_id == successor.id
    assert boss.status == "left"


@pytest.mark.django_db
def test_handover_to_self_rejected(admin_client, org_tree):
    _, _, sh = org_tree
    e = _emp("X1", "本人", sh)
    r = admin_client.post(
        f"/api/v1/org/employees/{e.id}/handover", {"to_employee": str(e.id)}, format="json"
    )
    assert r.status_code == 400


@pytest.mark.django_db
def test_overview_kpis(admin_client, org_tree):
    _, east, sh = org_tree
    _emp("A1", "甲", sh)
    _emp("A2", "乙", sh, status="left")
    ServiceArea.objects.create(organization=sh, area_type="deliver", region_name="上海市浦东新区")
    ServiceArea.objects.create(organization=sh, area_type="no_deliver", region_name="崇明区")

    r = admin_client.get("/api/v1/org/overview")
    assert r.status_code == 200
    d = r.json()["data"]
    assert d["organizations"]["total"] == 3
    assert d["organizations"]["by_property"].get("self", 0) >= 1
    assert d["employees"]["active"] == 1
    assert d["service_areas"]["total"] == 2
    assert d["service_areas"]["by_type"].get("deliver") == 1


@pytest.mark.django_db
def test_coverage_resolve_ranks_and_excludes(admin_client, org_tree):
    _, east, sh = org_tree
    wh = Organization.objects.create(code="WH", name="武汉网点", type="station", parent=east)
    # 上海网点：浦东派送(优先级20) + 崇明不派送
    ServiceArea.objects.create(organization=sh, area_type="deliver", region_name="上海市浦东新区", priority=20)
    ServiceArea.objects.create(organization=sh, area_type="no_deliver", region_name="上海市崇明区")
    # 武汉网点：浦东中转(优先级5) —— 同目的地但派送优先于中转、且优先级低
    ServiceArea.objects.create(organization=wh, area_type="transfer", region_name="上海市浦东新区", priority=5)

    r = admin_client.get("/api/v1/org/route-resolve", {"city": "上海市", "district": "浦东新区"})
    assert r.status_code == 200
    d = r.json()["data"]
    assert [x["organization_name"] for x in d["resolved"]] == ["上海网点", "武汉网点"]
    assert d["resolved"][0]["area_type"] == "deliver"

    # 崇明区：上海网点被不派送排除，无派送候选
    r2 = admin_client.get("/api/v1/org/route-resolve", {"city": "上海市", "district": "崇明区"})
    d2 = r2.json()["data"]
    assert d2["resolved"] == []
    assert any("上海网点" == e["organization_name"] for e in d2["excluded"])


@pytest.mark.django_db
def test_employee_list_skips_heavy_fields(admin_client, org_tree):
    _, _, sh = org_tree
    _emp("L1", "列表甲", sh)
    r = admin_client.get("/api/v1/org/employees?page_size=50")
    assert r.status_code == 200
    items = r.json()["data"]["items"]
    # 列表跳过逐行用户组聚合
    assert all(item["group_names"] is None for item in items)
