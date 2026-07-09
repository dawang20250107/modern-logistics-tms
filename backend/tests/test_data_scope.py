"""组织数据权限全域生效：运单/订单/异常/回单/财务按组织范围过滤。

验证非超管用户只能看到自己数据范围内的数据：org 档只见本组织，org_sub 档见子树，
超管全见；无运单归属的财务记录（org 为空）不误伤。
"""

from decimal import Decimal

import pytest
from django.core.cache import cache
from django.utils import timezone
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord
from apps.iam.models import Organization, Permission, Role, RoleAssignment
from apps.ops.models import ExceptionRecord, Order, Receipt, Waybill


@pytest.fixture(autouse=True)
def _clear_cache():
    cache.clear()
    yield
    cache.clear()


@pytest.fixture
def tree(db):
    hq = Organization.objects.create(code="HQ", name="总部", type="group")
    c1 = Organization.objects.create(code="C1", name="一部", type="dept", parent=hq)
    c2 = Organization.objects.create(code="C2", name="二部", type="dept", parent=hq)
    return hq, c1, c2


def _role(code, scope, perm_codes=()):
    role = Role.objects.create(code=code, name=code, data_scope=scope)
    if perm_codes:
        perms = [Permission.objects.get_or_create(code=c, defaults={"name": c})[0] for c in perm_codes]
        role.permissions.set(perms)
    return role


def _user(username, org, role):
    from django.contrib.auth import get_user_model

    u = get_user_model().objects.create_user(username=username, password="x", organization=org)
    RoleAssignment.objects.create(user=u, role=role, organization=org)
    return u


def _wb(no, org):
    return Waybill.objects.create(waybill_no=no, route_name="r", organization=org)


def _client(user):
    c = APIClient()
    c.force_authenticate(user=user)
    return c


@pytest.mark.django_db
def test_waybill_scope_org_vs_subtree(tree):
    hq, c1, c2 = tree
    _wb("WB-HQ", hq)
    _wb("WB-C1", c1)
    _wb("WB-C2", c2)

    role_org = _role("viewer_org", "org", ["waybill.view"])
    role_sub = _role("viewer_sub", "org_sub", ["waybill.view"])
    u_c1 = _user("u_c1", c1, role_org)
    u_hq = _user("u_hq", hq, role_sub)

    # C1 用户（org 档）只见本部运单
    r = _client(u_c1).get("/api/v1/waybills")
    assert {w["waybill_no"] for w in r.json()["data"]["items"]} == {"WB-C1"}

    # HQ 用户（org_sub 档）见整棵子树
    r2 = _client(u_hq).get("/api/v1/waybills")
    assert {w["waybill_no"] for w in r2.json()["data"]["items"]} == {"WB-HQ", "WB-C1", "WB-C2"}


@pytest.mark.django_db
def test_superuser_sees_all_waybills(tree):
    from django.contrib.auth import get_user_model

    hq, c1, c2 = tree
    _wb("WB-C1", c1)
    _wb("WB-C2", c2)
    su = get_user_model().objects.create_superuser(username="root", password="x")
    r = _client(su).get("/api/v1/waybills")
    assert {w["waybill_no"] for w in r.json()["data"]["items"]} == {"WB-C1", "WB-C2"}


@pytest.mark.django_db
def test_order_scoped_by_creator_org(tree):
    hq, c1, c2 = tree
    role_org = _role("vo", "org")
    u_c1 = _user("u_c1", c1, role_org)
    u_c2 = _user("u_c2", c2, _role("vo2", "org"))
    Order.objects.create(order_no="O-C1", created_by=u_c1)
    Order.objects.create(order_no="O-C2", created_by=u_c2)

    r = _client(u_c1).get("/api/v1/orders")
    assert {o["order_no"] for o in r.json()["data"]["items"]} == {"O-C1"}


@pytest.mark.django_db
def test_exception_and_receipt_scoped_by_waybill_org(tree):
    hq, c1, c2 = tree
    wb1, wb2 = _wb("WB-C1", c1), _wb("WB-C2", c2)
    ExceptionRecord.objects.create(waybill=wb1, exception_type="damage")
    ExceptionRecord.objects.create(waybill=wb2, exception_type="damage")
    Receipt.objects.create(waybill=wb1)
    Receipt.objects.create(waybill=wb2)

    u_c1 = _user("u_c1", c1, _role("vo", "org"))
    c = _client(u_c1)
    exc_wbs = {e["waybill"] for e in c.get("/api/v1/exceptions").json()["data"]["items"]}
    assert exc_wbs == {str(wb1.id)}
    rc = c.get("/api/v1/receipts").json()["data"]["items"]
    assert {r["waybill"] for r in rc} == {str(wb1.id)}


@pytest.mark.django_db
def test_expense_scope_includes_unattributed(tree):
    hq, c1, c2 = tree
    wb1, wb2 = _wb("WB-C1", c1), _wb("WB-C2", c2)
    wb_noorg = _wb("WB-NOORG", None)  # 运单无组织归属 → 费用视为无归属
    now = timezone.now()
    ExpenseRecord.objects.create(
        waybill=wb1, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
        expense_item_code="F", amount=Decimal("100"), occurred_at=now,
    )
    ExpenseRecord.objects.create(
        waybill=wb2, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
        expense_item_code="F", amount=Decimal("200"), occurred_at=now,
    )
    ExpenseRecord.objects.create(
        waybill=wb_noorg, direction=ExpenseRecord.DIRECTION_PAYABLE,
        expense_item_code="G", amount=Decimal("50"), occurred_at=now,
    )

    u_c1 = _user("u_c1", c1, _role("vo", "org"))
    items = _client(u_c1).get("/api/v1/finance/expense-records").json()["data"]["items"]
    seen = {(i["expense_item_code"], i["waybill"]) for i in items}
    # 见本部费用 + 无归属费用；不见 C2 的费用
    assert ("F", str(wb1.id)) in seen
    assert ("G", str(wb_noorg.id)) in seen
    assert ("F", str(wb2.id)) not in seen


@pytest.mark.django_db
def test_user_without_org_sees_nothing_scoped(tree):
    """有角色但无组织归属的用户：严格范围（运单）下看不到任何数据。"""
    hq, c1, c2 = tree
    _wb("WB-C1", c1)
    from django.contrib.auth import get_user_model

    role_org = _role("vo", "org", ["waybill.view"])
    u = get_user_model().objects.create_user(username="orphan", password="x")  # 无 organization
    RoleAssignment.objects.create(user=u, role=role_org)
    r = _client(u).get("/api/v1/waybills")
    assert r.json()["data"]["items"] == []
