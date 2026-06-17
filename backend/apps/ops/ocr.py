"""回单 OCR（可插拔）。

默认桩实现；真实可按 settings.OCR_PROVIDER 接入 PaddleOCR / 云 OCR，
只需替换 run_ocr 的实现并返回相同结构。
"""


def run_ocr(receipt) -> dict:
    source = receipt.file.name if receipt.file else receipt.file_url
    return {
        "provider": "stub",
        "confidence": 0.0,
        "source": source,
        "fields": {"signatory": "", "signed_at": None, "receipt_no": ""},
        "note": "OCR 占位实现：接入真实引擎后返回签收人/时间/单号与置信度。",
    }
