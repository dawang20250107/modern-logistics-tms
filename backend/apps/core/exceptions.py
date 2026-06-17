"""统一异常处理：把所有错误规范成 {success:false, data:null, error:{code,message,details}}。"""

from rest_framework.exceptions import APIException
from rest_framework.response import Response
from rest_framework.views import exception_handler as drf_exception_handler


class AppError(Exception):
    """业务异常：在视图/服务层抛出，自动转为规范错误响应。"""

    def __init__(self, code: str, message: str, status: int = 400, details=None):
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status
        self.details = details


def _error_body(code: str, message: str, details=None) -> dict:
    return {"success": False, "data": None, "error": {"code": code, "message": message, "details": details}}


def custom_exception_handler(exc, context):
    if isinstance(exc, AppError):
        response = Response(_error_body(exc.code, exc.message, exc.details), status=exc.status)
        response.exception = True
        return response

    response = drf_exception_handler(exc, context)
    if response is None:
        # 未识别异常：交给 Django 默认 500 处理（DEBUG 下显示堆栈）
        return None

    code = getattr(exc, "default_code", "error") if isinstance(exc, APIException) else "error"
    data = response.data
    details = None
    if isinstance(data, dict) and set(data.keys()) == {"detail"}:
        message = str(data["detail"])
    elif isinstance(data, (dict, list)):
        message = "请求参数校验失败"
        details = data
    else:
        message = str(data)

    response.data = _error_body(str(code), message, details)
    response.exception = True
    return response
