"""M5 报价归集与 Webhook 投递测试。"""

from decimal import Decimal

import pytest

from apps.finance.models import ExpenseRecord, PricingRule, Webhook, WebhookDelivery
from apps.finance.services import emit_event, generate_costs
from apps.masterdata.models import Customer
from apps.ops.models import Waybill


@pytest.mark.django_db
def test_generate_costs_from_pricing_rules():
    cust = Customer.objects.create(code="C1", name="c")
    wb = Waybill.objects.create(waybill_no="P1", route_name="A->B", customer=cust, cargo_weight_ton=Decimal("10"))
    PricingRule.objects.create(
        name="inc", price_type="income", expense_item_code="TRANSPORT_INCOME",
        base_price=Decimal("1000"), price_per_ton=Decimal("100"),
    )
    PricingRule.objects.create(
        name="cost", price_type="cost", expense_item_code="TRANSPORT_COST",
        base_price=Decimal("500"), price_per_ton=Decimal("50"),
    )

    result = generate_costs(wb)
    assert result == {"receivable": 1, "payable": 1}
    assert ExpenseRecord.objects.get(waybill=wb, direction="receivable").amount == Decimal("2000.00")
    assert ExpenseRecord.objects.get(waybill=wb, direction="payable").amount == Decimal("1000.00")

    # 再次生成应替换而非重复
    generate_costs(wb)
    assert ExpenseRecord.objects.filter(waybill=wb, source_system="pricing").count() == 2


@pytest.mark.django_db
def test_webhook_emit_creates_delivery():
    Webhook.objects.create(name="ext", target_url="http://127.0.0.1:9/none", secret="s", events="*")
    count = emit_event("test.event", {"a": 1})
    assert count == 1
    delivery = WebhookDelivery.objects.first()
    assert delivery is not None
    assert delivery.event_type == "test.event"
    assert delivery.attempts >= 1
    assert delivery.status in ("failed", "success")
