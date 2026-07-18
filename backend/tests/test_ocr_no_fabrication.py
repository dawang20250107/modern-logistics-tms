"""去假：无 OCR 引擎时，回单/证件 OCR 绝不伪造签收人、证件号、未来到期日。"""

import pytest

from apps.masterdata.credential_ocr import apply_ocr, match_driver, recognize
from apps.masterdata.models import Driver, DriverCredential
from apps.ops.models import Receipt, Waybill
from apps.ops.ocr import run_ocr


@pytest.mark.django_db
def test_receipt_ocr_does_not_fabricate_signatory():
    wb = Waybill.objects.create(waybill_no="NF1", route_name="r")
    receipt = Receipt.objects.create(waybill=wb, receipt_type="signed_pod")
    result = run_ocr(receipt)
    assert result["status"] == "manual"
    assert result["fields"] == {}  # 不造签收人/签收时间


@pytest.mark.django_db
def test_receipt_ocr_preserves_human_signatory():
    from apps.ops.tasks import process_receipt_ocr

    wb = Waybill.objects.create(waybill_no="NF2", route_name="r")
    receipt = Receipt.objects.create(waybill=wb, receipt_type="signed_pod", signatory="张三(人工)")
    process_receipt_ocr(str(receipt.id))
    receipt.refresh_from_db()
    assert receipt.signatory == "张三(人工)"  # 人工录入不被 OCR 覆盖
    assert receipt.ocr_status == "manual"


@pytest.mark.django_db
def test_credential_ocr_does_not_fabricate_expiry():
    drv = Driver.objects.create(name="李四", phone="13800000000", id_no="510104198805054321")
    cred = DriverCredential.objects.create(driver=drv, cred_type="driving_license", side="main")
    apply_ocr(cred)
    cred.refresh_from_db()
    assert cred.ocr_status == "manual"
    # 关键：不伪造未来到期日（此前会写 +6年，洗白过期证件、架空派车硬阻断）
    assert cred.expiry_date is None
    assert cred.cert_no == ""
    assert recognize(cred)["fields"]["expiry_date"] is None


@pytest.mark.django_db
def test_credential_ocr_preserves_verified_fields():
    from datetime import date

    drv = Driver.objects.create(name="王五", phone="13800000001", id_no="510104198805054322")
    cred = DriverCredential.objects.create(
        driver=drv, cred_type="driving_license", side="main",
        holder_name="王五", cert_no="ABC123", expiry_date=date(2030, 1, 1),
    )
    apply_ocr(cred)
    cred.refresh_from_db()
    # 人工已核验的持有人/证号/有效期一律不被 OCR 覆盖
    assert cred.holder_name == "王五"
    assert cred.cert_no == "ABC123"
    assert cred.expiry_date == date(2030, 1, 1)


@pytest.mark.django_db
def test_match_driver_rejects_ambiguous_and_short_tail():
    Driver.objects.create(name="重名", phone="1", id_no="110101199001011111")
    Driver.objects.create(name="重名", phone="2", id_no="220202199002022222")
    # 姓名唯一但尾号不足 6 位 → None
    assert match_driver(name="重名", id_tail="11") is None
    # 尾号非数字 → None
    assert match_driver(name="重名", id_tail="abcdef") is None
    # 姓名 + 精确 6 位唯一命中
    assert match_driver(name="重名", id_tail="011111").name == "重名"
