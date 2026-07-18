"""回单 OCR（可插拔）。

默认不接任何引擎——此时**绝不伪造**签收人/签收时间（伪造会污染 e-POD 法律凭证并
误导财务自动核销）。接入真实引擎：设 settings.OCR_PROVIDER 并实现下方 _provider_ocr，
返回 {"provider","status","source","fields":{signatory,signed_at,...}}。
"""

from django.conf import settings


def run_ocr(receipt) -> dict:
    """对回单执行 OCR。未配置引擎时返回待人工录入结果（status=manual），不造数。"""
    source = receipt.file.name if receipt.file else receipt.file_url
    provider = getattr(settings, "OCR_PROVIDER", "") or ""
    if not provider:
        return {
            "provider": "none",
            "status": "manual",
            "source": source,
            "fields": {},
            "note": "未配置回单 OCR 引擎，签收信息待人工录入/核验。",
        }
    return _provider_ocr(provider, receipt, source)


def _provider_ocr(provider: str, receipt, source) -> dict:
    """真实 OCR 引擎接入点（PaddleOCR/云 OCR）。未实现具体 provider 时按待人工处理。"""
    # 按 provider 分派到具体实现；识别成功 status=done 并回填 fields，失败 status=failed。
    return {
        "provider": provider,
        "status": "manual",
        "source": source,
        "fields": {},
        "note": f"OCR 引擎 {provider} 尚未接入实现，签收信息待人工录入。",
    }
