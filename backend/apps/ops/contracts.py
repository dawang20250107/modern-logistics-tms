"""合同库：承运合同生成（含中文 PDF）/ 发送 / 司机确认。

对应「运链」方案"调度完成→发合同→引导注册→司机确认"节点，告别文字版合同、有据可查。
微信发送为外部接入，当前预留（reserved），不阻断合同流转。
"""

import io

from django.core.files.base import ContentFile
from django.utils import timezone

from apps.core.exceptions import AppError
from apps.core.redis import publish_event

from .models import Contract, WaybillEvent
from .numbering import contract_no as gen_contract_no

_TEMPLATE = """运输承运合同

合同编号：{contract_no}
关联运单：{waybill_no}
签订日期：{date}

一、承运信息
  承运司机：{driver_name}    联系电话：{driver_phone}
  牵引车牌：{vehicle}    挂车牌：{trailer}

二、运输内容
  线路：{route}
  货物：{cargo_desc}    重量：{weight} 吨    件数：{quantity} 件

三、运费
  约定运费（应付）：人民币 {freight} 元

四、约定条款
  1. 承运方应按约定时间、线路完成提货与送货，确保货物安全。
  2. 全程接受平台 GPS 跟踪与节点回传，按要求上传回单。
  3. 异常情况应第一时间报备，责任与费用按平台规则处理。
  4. 本合同经司机线上确认即生效，与书面合同具有同等效力。

承运方（司机）确认：__________      平台（智运）：__________
"""


def _waybill_freight(waybill):
    from django.db.models import Sum

    from apps.finance.models import ExpenseRecord

    total = ExpenseRecord.objects.filter(
        waybill=waybill, direction=ExpenseRecord.DIRECTION_PAYABLE,
    ).aggregate(t=Sum("amount"))["t"]
    return total or 0


def render_contract_text(contract) -> str:
    wb = contract.waybill
    return _TEMPLATE.format(
        contract_no=contract.contract_no,
        waybill_no=wb.waybill_no,
        date=timezone.now().strftime("%Y-%m-%d"),
        driver_name=(wb.driver.name if wb.driver_id else "—"),
        driver_phone=(wb.driver.phone if wb.driver_id else "—"),
        vehicle=(wb.vehicle.plate_no if wb.vehicle_id else "—"),
        trailer=(wb.trailer.plate_no if wb.trailer_id else "—"),
        route=wb.route_name or f"{wb.origin}→{wb.destination}",
        cargo_desc=(wb.order.cargo_desc if wb.order_id and wb.order.cargo_desc else "见运单"),
        weight=wb.cargo_weight_ton,
        quantity=wb.cargo_quantity,
        freight=_waybill_freight(wb),
    )


def _render_pdf_bytes(text: str) -> bytes:
    """用 reportlab + 内置中文 CID 字体生成合同 PDF（无需外部字体文件）。"""
    from reportlab.lib.pagesizes import A4
    from reportlab.pdfbase import pdfmetrics
    from reportlab.pdfbase.cidfonts import UnicodeCIDFont
    from reportlab.pdfgen import canvas

    pdfmetrics.registerFont(UnicodeCIDFont("STSong-Light"))
    buf = io.BytesIO()
    c = canvas.Canvas(buf, pagesize=A4)
    width, height = A4
    c.setFont("STSong-Light", 11)
    x, y = 50, height - 60
    for line in text.split("\n"):
        if y < 60:
            c.showPage()
            c.setFont("STSong-Light", 11)
            y = height - 60
        c.drawString(x, y, line)
        y -= 20
    c.showPage()
    c.save()
    return buf.getvalue()


def _record(waybill, event_type, payload=None):
    WaybillEvent.objects.create(
        waybill=waybill, event_type=event_type, event_time=timezone.now(),
        resource=waybill.waybill_no, source="contract", payload=payload or {},
    )


def generate_contract(waybill, *, operator=None) -> Contract:
    """生成承运合同：填充模板 + 自动生成中文 PDF，状态为待发送。"""
    contract = Contract.objects.create(
        contract_no=gen_contract_no(timezone.now()),
        waybill=waybill,
        driver=waybill.driver,
        confirm_status=Contract.STATUS_PENDING,
    )
    contract.content = render_contract_text(contract)
    try:
        pdf_bytes = _render_pdf_bytes(contract.content)
        contract.pdf.save(f"{contract.contract_no}.pdf", ContentFile(pdf_bytes), save=False)
    except Exception:  # noqa: BLE001 — PDF 失败不阻断合同生成（保留文本内容）
        pass
    contract.save()
    _record(waybill, "contract_generated", {"contract_no": contract.contract_no})
    return contract


def send_contract(contract, *, operator=None) -> Contract:
    """发送合同给司机（微信下发为外部接入·预留）。"""
    if contract.confirm_status == Contract.STATUS_CONFIRMED:
        raise AppError("CONTRACT_CONFIRMED", "合同已确认，无需重复发送。", status=409)
    contract.sent_at = timezone.now()
    contract.confirm_status = Contract.STATUS_SENT
    contract.save(update_fields=["sent_at", "confirm_status", "updated_at"])
    # 微信下发预留：接入后在此推送合同 PDF 给司机
    from apps.integrations.wechat import send_contract_to_driver

    send_contract_to_driver(contract)
    _record(contract.waybill, "contract_sent", {"contract_no": contract.contract_no})
    publish_event("contract_sent", {"waybill_no": contract.waybill.waybill_no, "contract_no": contract.contract_no})
    return contract


def confirm_contract(contract, *, accepted=True, reply="", operator=None) -> Contract:
    """司机确认/拒签合同；确认后刷新司机累计统计。"""
    contract.confirm_status = Contract.STATUS_CONFIRMED if accepted else Contract.STATUS_REJECTED
    contract.confirmed_at = timezone.now()
    contract.driver_reply = reply
    contract.save(update_fields=["confirm_status", "confirmed_at", "driver_reply", "updated_at"])
    _record(contract.waybill, "contract_confirmed" if accepted else "contract_rejected",
            {"contract_no": contract.contract_no, "reply": reply})
    publish_event("contract_confirmed" if accepted else "contract_rejected",
                  {"waybill_no": contract.waybill.waybill_no, "contract_no": contract.contract_no})
    if accepted and contract.driver_id:
        from .stats import refresh_driver_stats

        refresh_driver_stats(contract.driver)
    return contract
