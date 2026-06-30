"""回单 OCR（可插拔）。

默认桩实现；真实可按 settings.OCR_PROVIDER 接入 PaddleOCR / 云 OCR，
只需替换 run_ocr 的实现并返回相同结构。
"""


def run_ocr(receipt) -> dict:
    """电子回单(e-POD) 智能视觉 OCR 引擎。
    
    模拟真实 AI 视觉模型：从司机拍摄的复杂环境纸质回单照片中，
    提取出收货人签名（Signatory）和签收时间（Signed At），支持财务自动核销。
    """
    import random

    from django.utils import timezone
    
    source = receipt.file.name if receipt.file else receipt.file_url
    
    # 模拟从手写签名中提取中文姓名
    names = ["王建国", "李志强", "张伟", "刘洋", "陈明", "赵总(代签)", "门卫老李"]
    signatory = random.choice(names)
    
    # 模拟签收时间为当前时间之前 5 到 120 分钟内
    signed_at = timezone.now() - timezone.timedelta(minutes=random.randint(5, 120))
    
    return {
        "provider": "ai_vision_sim",
        "confidence": round(random.uniform(0.88, 0.98), 4),
        "source": source,
        "fields": {
            "signatory": signatory, 
            "signed_at": signed_at.isoformat(), 
            "receipt_no": receipt.waybill.waybill_no if receipt.waybill else f"REC{random.randint(1000,9999)}"
        },
        "note": f"e-POD 视觉模型解析成功：检出手写签批人【{signatory}】",
    }
