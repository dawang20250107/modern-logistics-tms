from rest_framework import mixins, serializers, viewsets
from rest_framework.permissions import IsAdminUser

from .models import AuditLog


class AuditLogSerializer(serializers.ModelSerializer):
    actor_name = serializers.CharField(source="actor.username", read_only=True, default="")

    class Meta:
        model = AuditLog
        fields = [
            "id", "actor", "actor_name", "action", "resource_type", "resource_id",
            "request_id", "method", "path", "status_code", "ip", "payload", "created_at",
        ]


class AuditLogViewSet(mixins.ListModelMixin, mixins.RetrieveModelMixin, viewsets.GenericViewSet):
    """审计日志查询（只读，限管理员）。"""

    queryset = AuditLog.objects.select_related("actor").all()
    serializer_class = AuditLogSerializer
    permission_classes = [IsAdminUser]
    filterset_fields = ["action", "resource_type", "resource_id", "actor", "status_code", "method"]
    search_fields = ["action", "path", "resource_id", "request_id"]
    ordering_fields = ["created_at", "status_code"]
