"""统一响应信封：{success, data, error}。

成功响应由渲染器自动包裹；错误响应由 exceptions.custom_exception_handler 构造。
"""

from rest_framework.renderers import JSONRenderer

ENVELOPE_KEYS = frozenset({"success", "data", "error"})


def envelope(data=None, *, success: bool = True, error=None) -> dict:
    return {"success": success, "data": data, "error": error}


class EnvelopeJSONRenderer(JSONRenderer):
    """把视图返回的数据包成统一信封。

    - 异常响应：异常处理器已构造信封，透传；
    - 已是信封结构：透传（幂等，避免二次包裹）。
    """

    def render(self, data, accepted_media_type=None, renderer_context=None):
        renderer_context = renderer_context or {}
        response = renderer_context.get("response")

        if response is not None and getattr(response, "exception", False):
            return super().render(data, accepted_media_type, renderer_context)

        if isinstance(data, dict) and ENVELOPE_KEYS.issubset(data.keys()):
            return super().render(data, accepted_media_type, renderer_context)

        return super().render(envelope(data), accepted_media_type, renderer_context)
