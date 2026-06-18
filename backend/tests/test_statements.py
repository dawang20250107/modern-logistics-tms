"""对账单（生成 / 确认 / 差异稽核）测试。"""

from datetime import date, datetime
from decimal import Decimal

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord, Statement
from apps.finance.services import confirm_statement, generate_statement
from apps.masterdata.models import Customer
from apps.ops.models import Waybill


@pytest.fixture
def admin_client():
    get_user_model().objects.create_superuser(username="a", password="pw-strong-123")
    client = APIClient()
    resp = client.post("/api/v1/auth/token", {"username": "a", "password": "pw-strong-123"}, format="json")
    client.credentials(HTTP_AUTHORIZATION=f"Bearer {resp.json()['data']['access']}")
    return client


def _dt(y, m, d):
    return timezone.make_aware(datetime(y, m, d, 10, 0))


@pytest.mark.django_db
def test_generate_statement_aggregates_receivables():
    cust = Customer.objects.create(code="C1", name="客户甲")
    wb = Waybill.objects.create(waybill_no="WST1", route_name="r", customer=cust)
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="FREIGHT", amount=Decimal("1000"), occurred_at=_dt(2026, 6, 5))
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="FUEL", amount=Decimal("200"), occurred_at=_dt(2026, 6, 20))
    # 期外 / 反向，不计入
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="X", amount=Decimal("999"), occurred_at=_dt(2026, 7, 1))
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_PAYABLE, expense_item_code="Y", amount=Decimal("500"), occurred_at=_dt(2026, 6, 10))

    statement = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE,
        counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id),
        start=date(2026, 6, 1),
        end=date(2026, 6, 30),
        external_total=Decimal("1300"),
    )
    assert statement.total_amount == Decimal("1200")
    assert statement.item_count == 2
    assert statement.counterparty_name == "客户甲"
    assert statement.diff == Decimal("-100")  # 1200 - 1300
    assert statement.lines.count() == 2


@pytest.mark.django_db
def test_confirm_statement():
    cust = Customer.objects.create(code="C2", name="客户乙")
    statement = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE, counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id), start=date(2026, 6, 1), end=date(2026, 6, 30),
    )
    confirmed = confirm_statement(statement)
    assert confirmed.status == Statement.STATUS_CONFIRMED
    assert confirmed.confirmed_at is not None


@pytest.mark.django_db
def test_statement_generate_and_confirm_endpoints(admin_client):
    cust = Customer.objects.create(code="C3", name="客户丙")
    wb = Waybill.objects.create(waybill_no="WST3", route_name="r", customer=cust)
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="FREIGHT", amount=Decimal("800"), occurred_at=_dt(2026, 6, 15))

    resp = admin_client.post(
        "/api/v1/finance/statements/generate",
        {"direction": "receivable", "counterparty_type": "customer", "counterparty_id": str(cust.id),
         "period_start": "2026-06-01", "period_end": "2026-06-30"},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    sid = resp.json()["data"]["id"]
    assert resp.json()["data"]["total_amount"] == "800.00"

    resp = admin_client.post(f"/api/v1/finance/statements/{sid}/confirm")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["status"] == "confirmed"
