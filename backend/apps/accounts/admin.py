from django.contrib import admin
from django.contrib.auth.admin import UserAdmin as DjangoUserAdmin

from .models import User


@admin.register(User)
class UserAdmin(DjangoUserAdmin):
    list_display = ("username", "nickname", "phone", "organization", "is_staff", "is_active")
    search_fields = ("username", "nickname", "phone")
    fieldsets = DjangoUserAdmin.fieldsets + (("扩展信息", {"fields": ("nickname", "phone", "organization")}),)
