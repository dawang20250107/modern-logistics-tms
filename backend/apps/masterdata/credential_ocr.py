"""司机证件 OCR（可插拔）与按 姓名+身份证后6位 检索建档。

默认离线桩实现；接入真实 OCR（百度/腾讯/阿里云 或 PaddleOCR）后替换 recognize() 即可，
返回相同结构。证件类型 → 关注字段：
  身份证→姓名/身份证号；驾驶证→姓名/证号/有效期；行驶证→车牌/有效期；运输证→证号/有效期。
"""

from django.conf import settings


def recognize(credential) -> dict:
    """识别证件，返回 {provider, confidence, fields:{name, cert_no, plate_no, id_no, expiry_date}}。

    离线桩：不发起外部调用，返回空字段并标注，供接入真实引擎前流程跑通。
    """
    provider = getattr(settings, "OCR_PROVIDER", "") or "stub"
    source = credential.file.name if credential.file else credential.file_url
    return {
        "provider": provider,
        "confidence": 0.0,
        "source": source,
        "cred_type": credential.cred_type,
        "fields": {"name": "", "cert_no": "", "plate_no": "", "id_no": "", "expiry_date": None},
        "note": "证件 OCR 占位实现：接入真实引擎后自动带出姓名/证号/有效期。",
    }


def apply_ocr(credential) -> None:
    """对证件执行 OCR 并回填识别字段（姓名/证号/有效期）。"""
    result = recognize(credential)
    fields = result.get("fields", {})
    credential.ocr_result = result
    credential.ocr_status = "done"
    credential.holder_name = fields.get("name") or fields.get("plate_no") or credential.holder_name
    credential.cert_no = fields.get("cert_no") or credential.cert_no
    if fields.get("expiry_date"):
        credential.expiry_date = fields["expiry_date"]
    credential.save(update_fields=[
        "ocr_result", "ocr_status", "holder_name", "cert_no", "expiry_date", "updated_at",
    ])


def match_driver(name: str = "", id_tail: str = ""):
    """按 姓名 + 身份证后6位 检索司机（自动带出档案的关联键）。"""
    from .models import Driver

    qs = Driver.objects.all()
    if name:
        qs = qs.filter(name=name)
    if id_tail:
        qs = qs.filter(id_no__endswith=id_tail)
    if not name and not id_tail:
        return None
    return qs.first()
