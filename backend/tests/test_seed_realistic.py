"""实战演示数据命令：贯穿全流程造数，覆盖全部模块且可重复执行。"""

import pytest
from django.core.management import call_command


@pytest.mark.django_db
def test_seed_realistic_populates_all_modules():
    call_command("seed_realistic", orders=8)

    from apps.finance.models import ExpenseRecord, PricingRule, Reimbursement
    from apps.masterdata.models import Driver, DriverCredential, Vehicle
    from apps.ops.models import (
        Contract,
        DriverCheckin,
        DriverReminder,
        ExceptionRecord,
        Order,
        TrackingPoint,
        Waybill,
        WaybillStop,
    )

    # 全流程各模块均有数据
    assert Order.objects.count() >= 8
    assert Waybill.objects.exists()
    assert Contract.objects.exists()
    assert DriverReminder.objects.exists()
    assert DriverCheckin.objects.exists()
    assert TrackingPoint.objects.exists()
    assert WaybillStop.objects.exists()
    assert ExpenseRecord.objects.exists()
    assert Reimbursement.objects.exists()
    assert PricingRule.objects.exists()
    assert Driver.objects.exists()
    assert Vehicle.objects.filter(vehicle_class="trailer").exists()  # 含挂车
    assert DriverCredential.objects.filter(ocr_status="manual").exists()  # 证件待人工核验（不伪造）
    _ = ExceptionRecord  # 异常按概率生成，不强断言

    # 订单状态有多样性（既有完成也有在途/早期）
    statuses = set(Order.objects.values_list("status", flat=True))
    assert "completed" in statuses
    assert "converted" in statuses


@pytest.mark.django_db
def test_seed_realistic_idempotent_with_fresh():
    call_command("seed_realistic", orders=8)
    from apps.ops.models import Order

    first = Order.objects.count()
    call_command("seed_realistic", orders=8, fresh=True)  # 重复执行不报错、不暴涨
    assert Order.objects.count() == first
