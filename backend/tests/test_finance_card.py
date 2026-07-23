"""单票财务卡：应收/应付/其他/毛利 + 可对账判定（回单 + 异常）。"""

from decimal import Decimal

import pytest

from apps.finance.models import ExpenseRecord
from apps.ops.models import ExceptionRecord, Waybill


def _wb(receipt="returned"):
    return Waybill.objects.create(waybill_no="WBF-1", route_name="r", origin="上海", destination="杭州",
                                  status=Waybill.STATUS_SIGNED, receipt_status=receipt)


@pytest.mark.django_db
def test_finance_card_computes_gross_margin_and_reconcilable():
    wb = _wb(receipt="returned")
    ExpenseRecord.objects.create(waybill=wb, direction="receivable", expense_item_code="freight", amount=Decimal("3000"))
    ExpenseRecord.objects.create(waybill=wb, direction="payable", expense_item_code="freight", amount=Decimal("2200"))
    ExpenseRecord.objects.create(waybill=wb, direction="external", expense_item_code="platform_fee", amount=Decimal("100"))

    from apps.finance.services import waybill_finance_card

    card = waybill_finance_card(wb)
    assert card["receivable"] == 3000.0
    assert card["payable"] == 2200.0
    assert card["other_fee"] == 100.0
    assert card["gross_margin"] == 700.0
    assert card["receipt_ok"] is True
    assert card["reconcilable"] is True
    assert card["blockers"] == []


@pytest.mark.django_db
def test_finance_card_blocks_when_receipt_pending_or_exception():
    wb = _wb(receipt="pending")
    ExpenseRecord.objects.create(waybill=wb, direction="receivable", expense_item_code="freight", amount=Decimal("3000"))
    ExceptionRecord.objects.create(waybill=wb, exception_type="cargo_damage", level="high", amount=Decimal("500"))

    from apps.finance.services import waybill_finance_card

    card = waybill_finance_card(wb)
    assert card["reconcilable"] is False
    assert card["exception_deduction"] == 500.0
    assert "回单未回收" in card["blockers"]
    assert "存在未决异常" in card["blockers"]
