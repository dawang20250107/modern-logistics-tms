"""单据血缘：订单 → 运单 → 对账单 关系图测试。"""

from datetime import date, datetime
from decimal import Decimal

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord, Statement
from apps.finance.services import generate_statement
from apps.masterdata.models import Carrier, Customer
from apps.ops.lineage import order_lineage
from apps.ops.models import Order, Waybill


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
def test_order_lineage_chains_order_waybill_statement():
    cust = Customer.objects.create(code="LC1", name="血缘客户")
    carrier = Carrier.objects.create(code="LCR1", name="血缘承运商")
    order = Order.objects.create(order_no="DDL1", customer=cust, status="converted", quoted_amount=Decimal("1000"))
    wb = Waybill.objects.create(waybill_no="YDL1", route_name="r", order=order, customer=cust, carrier=carrier)
    ExpenseRecord.objects.create(waybill=wb, direction="receivable", expense_item_code="FREIGHT", amount=Decimal("1000"), occurred_at=_dt(2026, 6, 10))
    ExpenseRecord.objects.create(waybill=wb, direction="payable", expense_item_code="FREIGHT", amount=Decimal("800"), occurred_at=_dt(2026, 6, 10))

    # 生成客户应收对账单，费用应回链到该运单
    st = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE, counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id), start=date(2026, 6, 1), end=date(2026, 6, 30),
    )

    d = order_lineage(order)
    assert d["order"]["order_no"] == "DDL1"
    assert d["summary"]["waybill_count"] == 1
    assert d["summary"]["receivable_total"] == 1000.0
    assert d["summary"]["payable_total"] == 800.0
    assert d["summary"]["gross"] == 200.0

    w = d["waybills"][0]
    assert w["waybill_no"] == "YDL1"
    assert w["carrier_name"] == "血缘承运商"
    assert w["receivable"] == 1000.0 and w["payable"] == 800.0
    # 运单的应收落进了生成的对账单
    assert st.statement_no in [s["statement_no"] for s in w["statements"]]
    assert st.statement_no in [s["statement_no"] for s in d["ar_statements"]]
    assert d["ap_statements"] == []  # 未生成应付单


@pytest.mark.django_db
def test_order_lineage_endpoint(admin_client):
    cust = Customer.objects.create(code="LC2", name="血缘客户2")
    order = Order.objects.create(order_no="DDL2", customer=cust, status="pooled", quoted_amount=Decimal("500"))
    resp = admin_client.get(f"/api/v1/orders/{order.id}/lineage")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert data["order"]["order_no"] == "DDL2"
    assert data["waybills"] == []
    assert data["summary"]["waybill_count"] == 0
