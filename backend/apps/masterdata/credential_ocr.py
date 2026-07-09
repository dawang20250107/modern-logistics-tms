"""司机/车辆证件 OCR（可插拔）与按 姓名+身份证后6位 检索建档。

默认不接引擎——此时**绝不伪造**证件号/有效期。伪造未来到期日会污染合规数据、
把过期证件"洗"成永久有效，直接架空派车时的证件到期硬阻断（安全/合规事故）。
接入真实 OCR（百度/腾讯/阿里云 或 PaddleOCR）：设 settings.OCR_PROVIDER 并实现
_provider_recognize，返回同结构。OCR 结果仅作**建议**，有效期须人工核验后方可生效。
"""

from django.conf import settings

_EMPTY_FIELDS = {"name": "", "cert_no": "", "plate_no": "", "id_no": "", "expiry_date": None}


def recognize(credential) -> dict:
    """识别证件。未配置引擎时返回空字段 + status=manual（待人工录入/核验），不造数。"""
    source = credential.file.name if credential.file else credential.file_url
    provider = getattr(settings, "OCR_PROVIDER", "") or ""
    if not provider:
        return {
            "provider": "none",
            "status": "manual",
            "source": source,
            "cred_type": credential.cred_type,
            "fields": dict(_EMPTY_FIELDS),
            "note": "未配置证件 OCR 引擎，证件信息待人工录入/核验。",
        }
    return _provider_recognize(provider, credential, source)


def _provider_recognize(provider: str, credential, source) -> dict:
    """真实 OCR 引擎接入点。未实现具体 provider 时按待人工处理，绝不返回臆造字段。"""
    return {
        "provider": provider,
        "status": "manual",
        "source": source,
        "cred_type": credential.cred_type,
        "fields": dict(_EMPTY_FIELDS),
        "note": f"OCR 引擎 {provider} 尚未接入实现，证件信息待人工录入。",
    }


def apply_ocr(credential) -> None:
    """对证件执行 OCR 并（谨慎）回填。绝不覆盖人工已录入字段；有效期不自动写入
    合规字段（仅作建议存于 ocr_result），须人工核验后手动确认，杜绝伪造到期架空阻断。"""
    result = recognize(credential)
    fields = result.get("fields", {})
    credential.ocr_result = result
    credential.ocr_status = result.get("status", "done")
    # 仅在原字段为空时用 OCR 建议值填充（不覆盖人工录入）
    if not credential.holder_name:
        credential.holder_name = fields.get("name") or fields.get("plate_no") or ""
    if not credential.cert_no:
        credential.cert_no = fields.get("cert_no") or ""
    # 有效期：合规关键字段，不由 OCR 自动写入（避免伪造未来到期洗白过期证件）。
    # 真实引擎识别到的到期日留在 ocr_result 供人工核验后手动确认。
    credential.save(update_fields=[
        "ocr_result", "ocr_status", "holder_name", "cert_no", "updated_at",
    ])


def match_driver(name: str = "", id_tail: str = ""):
    """按 姓名 + 身份证后6位 检索司机。要求姓名与 6 位数字尾号同时具备且唯一命中，
    否则返回 None——避免仅凭姓名或短尾号把证件/运单错绑到他人。"""
    from .models import Driver

    name = (name or "").strip()
    id_tail = (id_tail or "").strip()
    # 必须同时提供姓名与恰好 6 位数字的身份证尾号
    if not name or not (id_tail.isdigit() and len(id_tail) == 6):
        return None
    qs = Driver.objects.filter(name=name, id_no__endswith=id_tail)
    if qs.count() != 1:  # 无匹配或多义 → 不猜
        return None
    return qs.first()
