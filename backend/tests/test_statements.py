"""对账单（生成 / 确认 / 差异稽核）测试。"""

from datetime import date, datetime
from decimal import Decimal

import pytest
from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework.test import APIClient

from apps.finance.models import ExpenseRecord, Statement, StatementPayment
from apps.finance.services import (
    audit_statement,
    confirm_statement,
    generate_statement,
    settle_statement,
    statement_overview,
)
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
def test_audit_statement_flags_high_deviation_line():
    cust = Customer.objects.create(code="C4", name="客户丁")
    wb = Waybill.objects.create(waybill_no="WST4", route_name="r", customer=cust)
    for amt in (Decimal("100"), Decimal("110"), Decimal("90"), Decimal("105")):
        ExpenseRecord.objects.create(
            waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
            expense_item_code="TOLL", amount=amt, occurred_at=_dt(2026, 6, 10),
        )
    outlier = ExpenseRecord.objects.create(
        waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
        expense_item_code="TOLL", amount=Decimal("500"), occurred_at=_dt(2026, 6, 11),
    )

    statement = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE, counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id), start=date(2026, 6, 1), end=date(2026, 6, 30),
    )
    assert statement.lines.count() == 5

    summary = audit_statement(statement)
    assert summary["total_lines"] == 5
    assert summary["anomaly_count"] == 1

    outlier_line = statement.lines.get(expense_record=outlier)
    assert outlier_line.is_anomaly is True
    assert outlier_line.baseline_avg == Decimal("101.25")
    assert outlier_line.deviation_pct > 0

    normal_line = statement.lines.exclude(expense_record=outlier).first()
    assert normal_line.is_anomaly is False

    outlier.refresh_from_db()
    assert outlier.risk_status == "high_deviation"

    statement.refresh_from_db()
    assert statement.audited_at is not None


@pytest.mark.django_db
def test_audit_statement_requires_minimum_samples():
    cust = Customer.objects.create(code="C5", name="客户戊")
    wb = Waybill.objects.create(waybill_no="WST5", route_name="r", customer=cust)
    ExpenseRecord.objects.create(
        waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
        expense_item_code="RARE_ITEM", amount=Decimal("9999"), occurred_at=_dt(2026, 6, 10),
    )
    statement = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE, counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id), start=date(2026, 6, 1), end=date(2026, 6, 30),
    )
    audit_statement(statement)
    line = statement.lines.get()
    assert line.is_anomaly is False
    assert line.baseline_avg is None


@pytest.mark.django_db
def test_audit_statement_endpoint(admin_client):
    cust = Customer.objects.create(code="C6", name="客户己")
    wb = Waybill.objects.create(waybill_no="WST6", route_name="r", customer=cust)
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="FREIGHT", amount=Decimal("800"), occurred_at=_dt(2026, 6, 15))
    statement = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE, counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id), start=date(2026, 6, 1), end=date(2026, 6, 30),
    )
    resp = admin_client.post(f"/api/v1/finance/statements/{statement.id}/audit")
    assert resp.status_code == 200, resp.content
    body = resp.json()["data"]
    assert body["total_lines"] == 1
    assert body["statement"]["audited_at"] is not None


@pytest.mark.django_db
def test_financial_dashboard_metrics_endpoint(admin_client):
    """回归：此前该端点按 ExpenseRecord.status=STATUS_CONFIRMED 过滤，但 ExpenseRecord
    既无 status 字段也无 STATUS_CONFIRMED 常量 → 每次调用必 500，看板主图从不渲染；
    且成本构成用错误的 icontains 编码 + 编造的降级演示数据兜底。"""
    cust = Customer.objects.create(code="FD1", name="看板客户")
    wb = Waybill.objects.create(waybill_no="WFD1", route_name="r", customer=cust)
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE, expense_item_code="TRANSPORT_INCOME", amount=Decimal("1000"))
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_PAYABLE, expense_item_code="FUEL_CARD", amount=Decimal("300"))
    ExpenseRecord.objects.create(waybill=wb, direction=ExpenseRecord.DIRECTION_PAYABLE, expense_item_code="TOLL", amount=Decimal("120"))

    resp = admin_client.get("/api/v1/finance/dashboard-metrics?days=7")
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"]
    assert len(data["trend"]) == 7
    assert any(row["revenue"] > 0 for row in data["trend"])
    # 成本构成按真实费用科目中文名聚合，无编造兜底
    comp = {c["name"]: c["value"] for c in data["cost_composition"]}
    assert comp["油卡"] == 300.0
    assert comp["过路费"] == 120.0
    # 此前的假兜底数字（45000/85000 等）不应再出现
    assert all(c["value"] in (300.0, 120.0) for c in data["cost_composition"])


@pytest.mark.django_db
def test_financial_dashboard_metrics_empty(admin_client):
    """无任何应付记录时成本构成为空列表，而非编造演示数据。"""
    resp = admin_client.get("/api/v1/finance/dashboard-metrics?days=7")
    assert resp.status_code == 200, resp.content
    assert resp.json()["data"]["cost_composition"] == []


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


def _confirmed_statement(code="CS1", name="核销客户", amount="1000"):
    cust = Customer.objects.create(code=code, name=name)
    wb = Waybill.objects.create(waybill_no=f"W{code}", route_name="r", customer=cust)
    ExpenseRecord.objects.create(
        waybill=wb, direction=ExpenseRecord.DIRECTION_RECEIVABLE,
        expense_item_code="FREIGHT", amount=Decimal(amount), occurred_at=_dt(2026, 6, 15),
    )
    st = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE, counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id), start=date(2026, 6, 1), end=date(2026, 6, 30),
    )
    return confirm_statement(st)


@pytest.mark.django_db
def test_settle_partial_then_full():
    st = _confirmed_statement(amount="1000")
    # 首次部分核销 400 → partial
    settle_statement(st, amount=Decimal("400"), method="bank", paid_at=date(2026, 7, 1))
    st.refresh_from_db()
    assert st.status == Statement.STATUS_PARTIAL
    assert st.settled_amount == Decimal("400")
    assert st.outstanding == Decimal("600")
    # 再核销剩余 600 → settled
    settle_statement(st, amount=Decimal("600"), method="cash", paid_at=date(2026, 7, 5))
    st.refresh_from_db()
    assert st.status == Statement.STATUS_SETTLED
    assert st.settled_amount == Decimal("1000")
    assert st.outstanding == Decimal("0")
    assert st.settled_at is not None
    assert StatementPayment.objects.filter(statement=st).count() == 2


@pytest.mark.django_db
def test_settle_rejects_overpay():
    from apps.core.exceptions import AppError

    st = _confirmed_statement(code="CS2", amount="500")
    with pytest.raises(AppError):
        settle_statement(st, amount=Decimal("600"))


@pytest.mark.django_db
def test_settle_requires_confirmed():
    """草稿单据不可核销。"""
    from apps.core.exceptions import AppError

    cust = Customer.objects.create(code="CS3", name="草稿客户")
    st = generate_statement(
        direction=Statement.DIRECTION_RECEIVABLE, counterparty_type=Statement.CP_CUSTOMER,
        counterparty_id=str(cust.id), start=date(2026, 6, 1), end=date(2026, 6, 30),
    )
    assert st.status == Statement.STATUS_DRAFT
    with pytest.raises(AppError):
        settle_statement(st, amount=Decimal("1"))


@pytest.mark.django_db
def test_settle_endpoint_and_payments(admin_client):
    st = _confirmed_statement(code="CS4", amount="800")
    resp = admin_client.post(
        f"/api/v1/finance/statements/{st.id}/settle",
        {"amount": "300", "method": "bank", "paid_at": "2026-07-02", "reference_no": "BANK-001"},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    data = resp.json()["data"]
    assert data["statement"]["status"] == "partial"
    assert data["statement"]["settled_amount"] == "300.00"
    assert data["statement"]["outstanding"] == "500.00"

    resp = admin_client.get(f"/api/v1/finance/statements/{st.id}/payments")
    assert resp.status_code == 200, resp.content
    payments = resp.json()["data"]
    assert len(payments) == 1
    assert payments[0]["amount"] == "300.00"
    assert payments[0]["reference_no"] == "BANK-001"


@pytest.mark.django_db
def test_statement_overview_endpoint(admin_client):
    st = _confirmed_statement(code="OV1", amount="1000")
    settle_statement(st, amount=Decimal("400"), paid_at=date(2026, 7, 1))

    resp = admin_client.get("/api/v1/finance/statement-overview")
    assert resp.status_code == 200, resp.content
    ov = resp.json()["data"]
    assert ov["receivable"]["total"] == 1000.0
    assert ov["receivable"]["settled"] == 400.0
    assert ov["receivable"]["outstanding"] == 600.0
    assert ov["receivable"]["partial"] == 1
    # net_position = AR未结 - AP未结
    assert ov["net_position"] == 600.0
    top = {r["counterparty_name"]: r["outstanding"] for r in ov["top_receivable"]}
    assert top.get("核销客户") == 600.0


@pytest.mark.django_db
def test_overview_service_direct():
    _confirmed_statement(code="OV2", amount="500")
    ov = statement_overview()
    assert ov["receivable"]["confirmed"] == 1
    assert ov["receivable"]["outstanding"] == 500.0
