"""车联网高危报警 → 自动异常工单联动。"""

import pytest
from django.utils import timezone

from apps.masterdata.models import Vehicle
from apps.ops.models import ExceptionRecord, Waybill
from apps.telematics.models import Alert
from apps.telematics.services import raise_alert


@pytest.mark.django_db
def test_high_alert_opens_exception():
    vehicle = Vehicle.objects.create(plate_no="沪X0001")
    wb = Waybill.objects.create(waybill_no="EXAUTO1", route_name="r", vehicle=vehicle)
    raise_alert(
        {"alert_type": Alert.TYPE_DEVIATION, "level": Alert.LEVEL_HIGH, "message": "偏离线路 9km"},
        vehicle=vehicle, waybill=wb, triggered_at=timezone.now(),
    )
    exc = ExceptionRecord.objects.filter(waybill=wb, exception_type="deviation")
    assert exc.count() == 1
    assert exc.first().source == "track"
    assert exc.first().description.startswith("[自动]")


@pytest.mark.django_db
def test_exception_deduped_per_waybill_type():
    vehicle = Vehicle.objects.create(plate_no="沪X0002")
    wb = Waybill.objects.create(waybill_no="EXAUTO2", route_name="r", vehicle=vehicle)
    for _ in range(2):
        raise_alert(
            {"alert_type": Alert.TYPE_OFFLINE, "level": Alert.LEVEL_HIGH, "message": "离线"},
            vehicle=vehicle, waybill=wb, triggered_at=timezone.now(), dedup=False,
        )
    assert ExceptionRecord.objects.filter(waybill=wb, exception_type="offline").count() == 1


@pytest.mark.django_db
def test_medium_alert_no_exception():
    vehicle = Vehicle.objects.create(plate_no="沪X0003")
    wb = Waybill.objects.create(waybill_no="EXAUTO3", route_name="r", vehicle=vehicle)
    raise_alert(
        {"alert_type": Alert.TYPE_OVERSPEED, "level": Alert.LEVEL_MEDIUM, "message": "超速"},
        vehicle=vehicle, waybill=wb, triggered_at=timezone.now(),
    )
    assert ExceptionRecord.objects.filter(waybill=wb).count() == 0
