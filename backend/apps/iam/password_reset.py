"""密码找回：验证码签发 / 校验 / 掩码 / 下发（短信·邮件网关预留）。

- 验证码存 Redis 缓存（10 分钟 TTL、一次性），不落库。
- 支持邮箱 / 手机号 / 用户名任一定位账号。
- send_reset_code 为预留下发点：现仅记录日志；接入企业微信/短信/邮件网关后在此实发。
"""

import logging
import secrets

from django.contrib.auth import get_user_model
from django.core.cache import cache

logger = logging.getLogger("auth")

CODE_TTL_SECONDS = 600
_KEY = "pwreset:{ident}"


def _norm(identifier: str) -> str:
    return (identifier or "").strip().lower()


def find_user(identifier: str):
    ident = (identifier or "").strip()
    if not ident:
        return None
    User = get_user_model()
    return (
        User.objects.filter(email__iexact=ident).first()
        or User.objects.filter(phone=ident).first()
        or User.objects.filter(username__iexact=ident).first()
    )


def issue_code(identifier: str) -> str:
    code = f"{secrets.randbelow(1_000_000):06d}"
    cache.set(_KEY.format(ident=_norm(identifier)), code, CODE_TTL_SECONDS)
    return code


def verify_code(identifier: str, code: str) -> bool:
    key = _KEY.format(ident=_norm(identifier))
    stored = cache.get(key)
    if stored and secrets.compare_digest(str(stored), str(code or "")):
        cache.delete(key)
        return True
    return False


def mask_target(user):
    """返回 (掩码后的发送目标, 渠道)。优先邮箱，其次手机号。"""
    if user.email:
        name, _, dom = user.email.partition("@")
        return f"{name[:2]}***@{dom}", "email"
    if user.phone and len(user.phone) >= 7:
        return f"{user.phone[:3]}****{user.phone[-4:]}", "phone"
    return None, None


def send_reset_code(user, code: str, channel: str) -> None:
    """预留下发点：接入短信/邮件/企业微信网关后在此实发；当前仅留痕。"""
    logger.info("[password-reset] user=%s channel=%s code=%s", user.username, channel, code)
