"""司机端极简：按运单状态给出唯一下一步动作。"""

import pytest

from apps.masterdata.models import Driver
from apps.ops.models import Waybill


def _wb(status):
    return Waybill.objects.create(waybill_no=f"WBD-{status}", route_name="r", origin="上海", destination="杭州", status=status)


@pytest.mark.django_db
@pytest.mark.parametrize("status,node,label", [
    (Waybill.STATUS_DISPATCHED, "loading", "确认装货"),
    (Waybill.STATUS_LOADED, "depart_loaded", "发车"),
    (Waybill.STATUS_DEPARTED, "in_transit", "在途打卡"),
    (Waybill.STATUS_IN_TRANSIT, "arrive_delivery", "到达卸货地"),
    (Waybill.STATUS_ARRIVED, "receipt", "上传回单"),
])
def test_next_step_by_status(status, node, label):
    from apps.ops.driver_portal import driver_next_step

    step = driver_next_step(_wb(status))
    assert step is not None
    assert step["node"] == node
    assert step["label"] == label


@pytest.mark.django_db
def test_next_step_none_when_finished():
    from apps.ops.driver_portal import driver_next_step

    assert driver_next_step(_wb(Waybill.STATUS_SIGNED)) is None


@pytest.mark.django_db
def test_driver_tasks_include_next_step():
    from django.test import Client

    from apps.ops.driver_portal import _issue_token

    drv = Driver.objects.create(name="王师傅", phone="13800138000", id_no="310000199001011234")
    Waybill.objects.create(waybill_no="WBD-T1", route_name="r", origin="上海", destination="杭州",
                           status=Waybill.STATUS_LOADED, driver=drv)
    token = _issue_token(drv)
    resp = Client().get("/api/v1/driver/tasks", HTTP_X_DRIVER_TOKEN=token)
    assert resp.status_code == 200, resp.content
    data = resp.json()["data"] if "data" in resp.json() else resp.json()
    wb = data["waybills"][0]
    assert wb["next_step"]["label"] == "发车"
    assert wb["status_label"] == "已装车"
