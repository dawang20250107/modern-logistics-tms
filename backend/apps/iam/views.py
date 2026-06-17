from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView


class MeView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        user = request.user
        roles = list(
            user.role_assignments.select_related("role").values_list("role__code", flat=True).distinct()
        )
        return Response(
            {
                "id": str(user.id),
                "username": user.username,
                "nickname": user.nickname,
                "phone": user.phone,
                "is_staff": user.is_staff,
                "is_superuser": user.is_superuser,
                "organization_id": str(user.organization_id) if user.organization_id else None,
                "roles": roles,
            }
        )
