"""司机端 H5（免登录门户）：手机号 + 身份证后6位登录，任务/提醒强制确认/打卡签到/证件上传。

登录签发短期 token（签名 driver_id），后续请求携带 token。打卡自动定位 + 水印照片。
"""

from django.core.files.base import ContentFile
from django.core.signing import BadSignature, SignatureExpired, TimestampSigner
from django.utils import timezone
from rest_framework.permissions import AllowAny
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.core.exceptions import AppError
from apps.core.throttling import DriverLoginRateThrottle

from .models import DriverCheckin, DriverReminder, Waybill

_SIGNER = TimestampSigner(salt="driver-portal")
_TOKEN_MAX_AGE = 7 * 24 * 3600


def _coord(value):
    """坐标容错：非法/越界返回 None，避免脏数据导致 500。"""
    try:
        v = float(value)
    except (TypeError, ValueError):
        return None
    return v if -180.0 <= v <= 180.0 else None
_ACTIVE_WB = [
    Waybill.STATUS_DISPATCHED, Waybill.STATUS_LOADED, Waybill.STATUS_DEPARTED,
    Waybill.STATUS_IN_TRANSIT, Waybill.STATUS_ARRIVED, Waybill.STATUS_PENDING_DISPATCH,
]


def _issue_token(driver) -> str:
    return _SIGNER.sign(str(driver.id))


def _driver_from_token(request):
    from apps.masterdata.models import Driver

    token = request.headers.get("X-Driver-Token") or request.query_params.get("token") or request.data.get("token")
    if not token:
        raise AppError("DRIVER_AUTH", "请先登录司机端。", status=401)
    try:
        driver_id = _SIGNER.unsign(token, max_age=_TOKEN_MAX_AGE)
    except SignatureExpired as exc:
        raise AppError("DRIVER_TOKEN_EXPIRED", "登录已过期，请重新登录。", status=401) from exc
    except BadSignature as exc:
        raise AppError("DRIVER_TOKEN_INVALID", "登录凭证无效。", status=401) from exc
    driver = Driver.objects.filter(id=driver_id).first()
    if driver is None:
        raise AppError("DRIVER_NOT_FOUND", "司机不存在。", status=404)
    return driver


class _DriverPublic(APIView):
    authentication_classes: list = []
    permission_classes = [AllowAny]


class DriverLoginView(_DriverPublic):
    """手机号 + 身份证后6位 登录（双因子：两者必填且必须同时匹配）。"""

    throttle_classes = [DriverLoginRateThrottle]

    def post(self, request):
        from apps.masterdata.models import Driver

        phone = (request.data.get("phone") or "").strip()
        id_tail = (request.data.get("id_tail") or "").strip()
        # 必须同时提供手机号与身份证后6位（防止仅凭手机号登录）
        if not phone or not id_tail:
            raise AppError("DRIVER_LOGIN_REQUIRED", "请输入手机号与身份证后 6 位。", status=400)
        if not id_tail.isdigit() or len(id_tail) != 6:
            raise AppError("DRIVER_LOGIN_REQUIRED", "身份证后 6 位格式不正确。", status=400)
        driver = Driver.objects.filter(phone=phone).first()
        # 始终校验身份证后6位；档案缺身份证号则无法验证身份，拒绝登录
        if driver is None or not driver.id_no or not driver.id_no.endswith(id_tail):
            raise AppError("DRIVER_LOGIN_FAILED", "手机号或身份证后 6 位不匹配。", status=401)
        return Response({
            "token": _issue_token(driver),
            "driver": {"id": str(driver.id), "name": driver.name, "phone": driver.phone,
                       "app_registered": driver.app_registered},
        })


# 司机极简流：按运单状态给出唯一"下一步动作"（司机只点一个主按钮，不选节点）
_NEXT_STEP = {
    Waybill.STATUS_PENDING_DISPATCH: {"node": "loading", "label": "确认装货", "kind": "checkin"},
    Waybill.STATUS_DISPATCHED: {"node": "loading", "label": "确认装货", "kind": "checkin"},
    Waybill.STATUS_LOADED: {"node": "depart_loaded", "label": "发车", "kind": "checkin"},
    Waybill.STATUS_DEPARTED: {"node": "in_transit", "label": "在途打卡", "kind": "checkin"},
    Waybill.STATUS_IN_TRANSIT: {"node": "arrive_delivery", "label": "到达卸货地", "kind": "checkin"},
    Waybill.STATUS_ARRIVED: {"node": "receipt", "label": "上传回单", "kind": "receipt"},
}


def driver_next_step(wb) -> dict | None:
    """当前运单对司机而言的唯一下一步动作；已签收/结算等无后续则返回 None。"""
    return _NEXT_STEP.get(wb.status)


def _waybill_brief(wb) -> dict:
    return {
        "waybill_no": wb.waybill_no, "route_name": wb.route_name,
        "origin": wb.origin, "destination": wb.destination, "status": wb.status,
        "status_label": dict(Waybill.STATUS_CHOICES).get(wb.status, wb.status),
        "pickup_address": getattr(wb.order, "pickup_address", "") if wb.order_id else "",
        "delivery_address": getattr(wb.order, "delivery_address", "") if wb.order_id else "",
        "pickup_contact_phone": getattr(wb.order, "pickup_contact_phone", "") if wb.order_id else "",
        "delivery_contact_phone": getattr(wb.order, "delivery_contact_phone", "") if wb.order_id else "",
        "next_step": driver_next_step(wb),
        "cod_amount": float(getattr(wb, "cod_amount", 0) or 0),
    }


class DriverTasksView(_DriverPublic):
    """司机任务：在途运单 + 待确认提醒（强制弹窗）。"""

    def get(self, request):
        driver = _driver_from_token(request)
        waybills = Waybill.objects.filter(driver=driver, status__in=_ACTIVE_WB).select_related("order")
        reminders = DriverReminder.objects.filter(
            driver=driver, status=DriverReminder.STATUS_PENDING,
        ).select_related("waybill")
        return Response({
            "driver": {"name": driver.name, "phone": driver.phone},
            "waybills": [_waybill_brief(w) for w in waybills],
            "pending_reminders": [
                {"id": str(r.id), "title": r.title, "content": r.content,
                 "ack_required": r.ack_required, "waybill_no": r.waybill.waybill_no if r.waybill_id else ""}
                for r in reminders
            ],
        })


class DriverAckReminderView(_DriverPublic):
    """司机确认收到提醒（强制弹窗点击）。"""

    def post(self, request, reminder_id=None):
        from .reminders import acknowledge_reminder

        driver = _driver_from_token(request)
        reminder = DriverReminder.objects.filter(id=reminder_id, driver=driver).first()
        if reminder is None:
            raise AppError("REMINDER_NOT_FOUND", "提醒不存在。", status=404)
        acknowledge_reminder(reminder)
        return Response({"ok": True, "status": reminder.status})


class DriverCheckinView(_DriverPublic):
    """打卡签到：节点 + 自动定位 + 水印照片。"""

    def post(self, request):
        from .watermark import watermark

        driver = _driver_from_token(request)
        wb = Waybill.objects.filter(waybill_no=request.data.get("waybill_no"), driver=driver).first()
        if wb is None:
            raise AppError("WAYBILL_NOT_FOUND", "运单不存在或非本人运单。", status=404)
        node = request.data.get("node")
        if node not in dict(DriverCheckin.NODE_CHOICES):
            raise AppError("INVALID_NODE", "打卡节点非法。", status=400)
        lat = _coord(request.data.get("lat"))
        lng = _coord(request.data.get("lng"))
        checkin = DriverCheckin(
            waybill=wb, driver=driver, node=node, lat=lat, lng=lng,
            note=(request.data.get("note", "") or "")[:255],
        )
        photo = request.FILES.get("photo")
        if photo:
            label = dict(DriverCheckin.NODE_CHOICES)[node]
            lines = [
                f"{timezone.now():%Y-%m-%d %H:%M:%S}",
                f"GPS {lat or '-'},{lng or '-'}",
                f"{label} · {driver.name} · {wb.waybill_no}",
            ]
            stamped = watermark(photo.read(), lines)
            checkin.photo.save(f"{wb.waybill_no}_{node}.jpg", ContentFile(stamped), save=False)
        checkin.save()
        # 工作流编排：打卡节点自动推进运单状态
        from .workflow import advance_from_checkin

        new_status = advance_from_checkin(wb, node, operator=None)
        return Response({"ok": True, "node": node, "checkin_at": checkin.checkin_at,
                         "waybill_status": new_status}, status=201)


class DriverCredentialUploadView(_DriverPublic):
    """司机自助上传证件（自传）。"""

    def post(self, request):
        from apps.masterdata.credential_ocr import apply_ocr
        from apps.masterdata.models import DriverCredential

        driver = _driver_from_token(request)
        cred_type = request.data.get("cred_type")
        if cred_type not in dict(DriverCredential.CRED_TYPE_CHOICES):
            raise AppError("INVALID_CRED_TYPE", "证件类型非法。", status=400)
        cred = DriverCredential(
            driver=driver, cred_type=cred_type, side=request.data.get("side", "main"),
            self_uploaded=True,
        )
        photo = request.FILES.get("file")
        if photo:
            cred.file.save(photo.name, photo, save=False)
        cred.save()
        apply_ocr(cred)
        return Response({"ok": True, "id": str(cred.id), "ocr_status": cred.ocr_status}, status=201)
