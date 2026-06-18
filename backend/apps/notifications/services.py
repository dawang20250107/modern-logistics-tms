"""通知发送：按用户/角色扇出，并推 SSE 实时响铃。失败不影响主流程。"""

from apps.core.redis import publish_event

from .models import Notification


def notify_users(user_ids, *, category, title, body="", level=Notification.LEVEL_INFO,
                 link_type="", link_id="", **payload):
    user_ids = list({uid for uid in user_ids if uid})
    if not user_ids:
        return 0
    objs = [
        Notification(
            recipient_id=uid, category=category, title=title, body=body, level=level,
            link_type=link_type, link_id=link_id, payload=payload,
        )
        for uid in user_ids
    ]
    Notification.objects.bulk_create(objs)
    publish_event("notification", {"category": category, "title": title, "recipients": len(user_ids)})
    return len(user_ids)


def notify_user(user, **kwargs):
    if not (user and getattr(user, "is_authenticated", False)):
        return 0
    return notify_users([user.id], **kwargs)


def notify_role(role_code, **kwargs):
    """按角色 code 扇出给该角色下所有用户。"""
    try:
        from apps.iam.models import RoleAssignment

        ids = RoleAssignment.objects.filter(role__code=role_code).values_list("user_id", flat=True)
        return notify_users(ids, **kwargs)
    except Exception:  # noqa: BLE001 - 通知非关键路径
        return 0
