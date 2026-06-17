"""对外 API-Key + HMAC 签名鉴权（防重放）。

客户端请求头：
  X-Api-Key:    公开 key_id
  X-Timestamp:  Unix 秒（与服务端时差需在窗口内）
  X-Signature:  hex( HMAC-SHA256(secret, canonical) )
  canonical = "METHOD\nPATH\nTIMESTAMP\nsha256hex(body)"
"""

import hashlib
import hmac
import time

from rest_framework.authentication import BaseAuthentication
from rest_framework.exceptions import AuthenticationFailed

REPLAY_WINDOW_SECONDS = 300


class ServicePrincipal:
    """API-Key 主体，鸭子类型兼容 DRF request.user。"""

    def __init__(self, api_key):
        self.api_key = api_key
        self.is_authenticated = True
        self.is_active = True
        self.is_staff = False
        self.is_superuser = False
        self.is_anonymous = False
        self.pk = f"apikey:{api_key.key_id}"
        self.username = api_key.name
        self.organization = api_key.organization

    def __str__(self):
        return self.pk


def build_canonical(method: str, path: str, timestamp: str, body: bytes) -> str:
    body_hash = hashlib.sha256(body or b"").hexdigest()
    return f"{method}\n{path}\n{timestamp}\n{body_hash}"


def sign(secret: str, canonical: str) -> str:
    return hmac.new(secret.encode(), canonical.encode(), hashlib.sha256).hexdigest()


class HMACAuthentication(BaseAuthentication):
    def authenticate(self, request):
        key_id = request.headers.get("X-Api-Key")
        if not key_id:
            return None  # 非本鉴权方式，交给后续认证器

        timestamp = request.headers.get("X-Timestamp", "")
        signature = request.headers.get("X-Signature", "")
        if not timestamp or not signature:
            raise AuthenticationFailed("缺少 X-Timestamp 或 X-Signature。")
        try:
            ts = int(timestamp)
        except ValueError as exc:
            raise AuthenticationFailed("X-Timestamp 无效。") from exc
        if abs(int(time.time()) - ts) > REPLAY_WINDOW_SECONDS:
            raise AuthenticationFailed("请求时间戳超出允许窗口（防重放）。")

        from .models import ApiKey

        api_key = ApiKey.objects.filter(key_id=key_id, is_active=True).select_related("organization").first()
        if api_key is None:
            raise AuthenticationFailed("无效的 API Key。")

        canonical = build_canonical(request.method, request.path, timestamp, request.body)
        expected = sign(api_key.secret, canonical)
        if not hmac.compare_digest(expected, signature):
            raise AuthenticationFailed("签名校验失败。")

        return (ServicePrincipal(api_key), api_key)

    def authenticate_header(self, request):
        return "HMAC"
