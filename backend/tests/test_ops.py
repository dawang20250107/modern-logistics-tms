"""运单状态机测试。"""

import pytest

from apps.core.exceptions import AppError
from apps.ops.models import Waybill
from apps.ops.services import allowed_next, transition_waybill


@pytest.mark.django_db
def test_valid_transition_writes_event():
    wb = Waybill.objects.create(
        waybill_no="T1", route_name="r", status=Waybill.STATUS_PENDING_DISPATCH
    )
    transition_waybill(wb, Waybill.STATUS_DISPATCHED, remark="go")
    assert wb.status == Waybill.STATUS_DISPATCHED
    assert wb.events.filter(event_type="status_changed:dispatched").exists()


@pytest.mark.django_db
def test_invalid_transition_rejected():
    wb = Waybill.objects.create(
        waybill_no="T2", route_name="r", status=Waybill.STATUS_DISPATCHED
    )
    with pytest.raises(AppError):
        transition_waybill(wb, Waybill.STATUS_DELIVERED)


def test_allowed_next_includes_void_from_dispatched():
    nexts = allowed_next(Waybill.STATUS_DISPATCHED)
    assert Waybill.STATUS_LOADED in nexts
    assert Waybill.STATUS_VOIDED in nexts
