"""运单签收环补齐：整签 / 部分签收（货损货差）/ 拒收，含状态机与自动立案。"""

from decimal import Decimal

import pytest
from rest_framework.test import APIClient

from apps.iam.models import Permission, Role, RoleAssignment
from apps.ops.models import ExceptionRecord, Receipt, Waybill


@pytest.fixture
def admin_client(db):
    from django.contrib.auth import get_user_model

    get_user_model().objects.create_superuser(username="ops_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "ops_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


def _wb(no, status=Waybill.STATUS_ARRIVED):
    return Waybill.objects.create(waybill_no=no, route_name="r", status=status)


@pytest.mark.django_db
def test_partial_sign_records_quantities_and_opens_exception(admin_client):
    wb = _wb("PS1")
    r = admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/partial-sign",
        {"total_quantity": 100, "signed_quantity": 80, "damaged_quantity": 5, "shortage_quantity": 15,
         "signatory": "王收货", "note": "外箱破损"},
        format="json",
    )
    assert r.status_code == 201, r.content
    data = r.json()["data"]
    assert data["status"] == Waybill.STATUS_PARTIALLY_SIGNED
    receipt = data["receipt"]
    assert receipt["outcome"] == "partial"
    assert Decimal(receipt["signed_quantity"]) == 80
    assert Decimal(receipt["damaged_quantity"]) == 5
    assert Decimal(receipt["shortage_quantity"]) == 15
    # 自动立货损异常（货损5+短少15=20 ≤ 100/2 → 中级）
    exc = ExceptionRecord.objects.get(waybill=wb)
    assert exc.exception_type == "cargo_damage"
    assert exc.level == "medium"
    # 订单未完成（无关联订单，验证运单态即可）
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_PARTIALLY_SIGNED
    assert wb.receipt_status == "received"


@pytest.mark.django_db
def test_partial_sign_high_level_when_over_half_damaged(admin_client):
    wb = _wb("PS2")
    admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/partial-sign",
        {"total_quantity": 100, "signed_quantity": 40, "damaged_quantity": 30, "shortage_quantity": 30},
        format="json",
    )
    exc = ExceptionRecord.objects.get(waybill=wb)
    assert exc.level == "high"  # 60 > 50


@pytest.mark.django_db
def test_partial_sign_rejects_full_quantity(admin_client):
    wb = _wb("PS3")
    # 无短少无货损 → 不该走部分签收
    r = admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/partial-sign",
        {"total_quantity": 100, "signed_quantity": 100}, format="json",
    )
    assert r.status_code == 400


@pytest.mark.django_db
def test_partial_sign_rejects_over_receive(admin_client):
    wb = _wb("PS4")
    r = admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/partial-sign",
        {"total_quantity": 100, "signed_quantity": 120}, format="json",
    )
    assert r.status_code == 400


@pytest.mark.django_db
def test_reject_waybill_sets_status_and_opens_complaint(admin_client):
    wb = _wb("RJ1")
    r = admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/reject",
        {"reason": "货物与订单不符，收货方拒收", "signatory": "李门卫"}, format="json",
    )
    assert r.status_code == 201, r.content
    data = r.json()["data"]
    assert data["status"] == Waybill.STATUS_REJECTED
    assert data["receipt"]["outcome"] == "rejected"
    exc = ExceptionRecord.objects.get(waybill=wb)
    assert exc.exception_type == "customer_complaint"
    assert exc.level == "high"
    r2 = Receipt.objects.get(waybill=wb)
    assert r2.status == "rejected"


@pytest.mark.django_db
def test_reject_requires_reason(admin_client):
    wb = _wb("RJ2")
    r = admin_client.post(f"/api/v1/waybills/{wb.waybill_no}/reject", {"reason": ""}, format="json")
    assert r.status_code == 400


@pytest.mark.django_db
def test_cannot_reject_signed_waybill(admin_client):
    wb = _wb("RJ3", status=Waybill.STATUS_SIGNED)
    r = admin_client.post(f"/api/v1/waybills/{wb.waybill_no}/reject", {"reason": "晚了"}, format="json")
    assert r.status_code == 409


@pytest.mark.django_db
def test_partial_then_full_sign_transition_allowed(admin_client):
    wb = _wb("PS5")
    admin_client.post(
        f"/api/v1/waybills/{wb.waybill_no}/partial-sign",
        {"total_quantity": 100, "signed_quantity": 80, "shortage_quantity": 20}, format="json",
    )
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_PARTIALLY_SIGNED
    # 部分签收 → 整签（补签剩余）应被状态机允许
    r = admin_client.post(f"/api/v1/waybills/{wb.waybill_no}/sign", {"signatory": "王收货"}, format="json")
    assert r.status_code == 201, r.content
    wb.refresh_from_db()
    assert wb.status == Waybill.STATUS_SIGNED


@pytest.mark.django_db
def test_partial_sign_requires_permission(db):
    """无 waybill.manage 权限的用户不得部分签收（403）。"""
    from django.contrib.auth import get_user_model

    wb = _wb("PS6")
    role = Role.objects.create(code="viewer", name="viewer", data_scope="all")
    role.permissions.set([Permission.objects.create(code="waybill.view", name="查看运单")])
    user = get_user_model().objects.create_user(username="viewer1", password="x")
    RoleAssignment.objects.create(user=user, role=role)
    c = APIClient()
    c.force_authenticate(user=user)
    r = c.post(
        f"/api/v1/waybills/{wb.waybill_no}/partial-sign",
        {"total_quantity": 10, "signed_quantity": 5, "shortage_quantity": 5}, format="json",
    )
    assert r.status_code == 403
