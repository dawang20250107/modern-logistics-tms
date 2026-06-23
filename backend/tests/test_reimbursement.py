"""内部简易报销：提交 → 审批(生成应付+付款) → 付款，计入经营结果。"""

import pytest

from apps.finance.models import ExpenseRecord, PaymentRequest, Reimbursement
from apps.ops.models import Order, Waybill


@pytest.fixture
def client(db):
    from django.contrib.auth import get_user_model
    from rest_framework.test import APIClient

    get_user_model().objects.create_superuser(username="bx_admin", password="pw-strong-123456")
    c = APIClient()
    tok = c.post("/api/v1/auth/token", {"username": "bx_admin", "password": "pw-strong-123456"}, format="json")
    c.credentials(HTTP_AUTHORIZATION=f"Bearer {tok.json()['data']['access']}")
    return c


@pytest.mark.django_db
def test_reimbursement_full_flow(client):
    order = Order.objects.create(order_no="DDBX1", origin="上海", destination="成都")
    wb = Waybill.objects.create(waybill_no="WBBX1", route_name="r", order=order)

    sub = client.post("/api/v1/finance/reimbursements", {
        "waybill_no": "WBBX1", "category": "toll", "amount": "320", "reason": "高速过路费",
    }, format="json")
    assert sub.status_code == 201, sub.content
    rid = sub.json()["data"]["id"]
    assert sub.json()["data"]["reimb_no"].startswith("BX")
    assert sub.json()["data"]["order_no"] == "DDBX1"  # 勾选订单带入
    assert sub.json()["data"]["status"] == "submitted"

    appr = client.post(f"/api/v1/finance/reimbursements/{rid}/approve", {}, format="json")
    assert appr.status_code == 200
    assert appr.json()["data"]["status"] == "approved"
    # 审批通过 → 应付费用（经营结果）+ 付款申请（下游付款跟进）
    assert ExpenseRecord.objects.filter(waybill=wb, source_system="reimbursement", amount=320).exists()
    assert PaymentRequest.objects.filter(counterparty_type="reimbursement", amount=320).exists()

    pay = client.post(f"/api/v1/finance/reimbursements/{rid}/pay", {}, format="json")
    assert pay.status_code == 200
    assert pay.json()["data"]["status"] == "paid"
    reimb = Reimbursement.objects.get(id=rid)
    assert reimb.payment_request.status == "paid"


@pytest.mark.django_db
def test_reimbursement_reject(client):
    Waybill.objects.create(waybill_no="WBBX2", route_name="r")
    sub = client.post("/api/v1/finance/reimbursements", {"waybill_no": "WBBX2", "category": "fuel", "amount": "500"}, format="json")
    rid = sub.json()["data"]["id"]
    rej = client.post(f"/api/v1/finance/reimbursements/{rid}/reject", {"reason": "超标"}, format="json")
    assert rej.status_code == 200
    assert rej.json()["data"]["status"] == "rejected"
    # 驳回不生成付款
    assert not PaymentRequest.objects.filter(amount=500).exists()


@pytest.mark.django_db
def test_reimbursement_amount_required(client):
    Waybill.objects.create(waybill_no="WBBX3", route_name="r")
    resp = client.post("/api/v1/finance/reimbursements", {"waybill_no": "WBBX3", "category": "other", "amount": "0"}, format="json")
    assert resp.status_code == 400


@pytest.mark.django_db
def test_cannot_pay_unapproved(client):
    Waybill.objects.create(waybill_no="WBBX4", route_name="r")
    sub = client.post("/api/v1/finance/reimbursements", {"waybill_no": "WBBX4", "category": "other", "amount": "100"}, format="json")
    rid = sub.json()["data"]["id"]
    assert client.post(f"/api/v1/finance/reimbursements/{rid}/pay", {}, format="json").status_code == 409
