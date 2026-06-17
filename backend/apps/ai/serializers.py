from rest_framework import serializers

from .models import AgentSuggestion


class AgentSuggestionSerializer(serializers.ModelSerializer):
    waybill_no = serializers.CharField(source="waybill.waybill_no", read_only=True, default="")

    class Meta:
        model = AgentSuggestion
        fields = [
            "id", "waybill", "waybill_no", "suggestion_type", "title", "body", "status",
            "evidence", "tool_name", "confirmed_at", "created_at",
        ]
        read_only_fields = ["confirmed_at"]
