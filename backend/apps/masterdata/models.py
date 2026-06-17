"""主数据：客户 / 承运商 / 车辆 / 司机。"""

from django.db import models

from apps.core.models import BaseModel, SoftDeleteModel


class Customer(BaseModel, SoftDeleteModel):
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    settlement_type = models.CharField(max_length=32, blank=True)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_customer"
        ordering = ["code"]
        verbose_name = "客户"
        verbose_name_plural = "客户"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class Carrier(BaseModel, SoftDeleteModel):
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    contact_name = models.CharField(max_length=64, blank=True)
    contact_phone = models.CharField(max_length=32, blank=True)
    settlement_type = models.CharField(max_length=32, blank=True)
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_carrier"
        ordering = ["code"]
        verbose_name = "承运商"
        verbose_name_plural = "承运商"

    def __str__(self) -> str:
        return f"{self.code} {self.name}"


class Vehicle(BaseModel, SoftDeleteModel):
    plate_no = models.CharField(max_length=32, unique=True)
    vehicle_type = models.CharField(max_length=64, blank=True)
    ownership_type = models.CharField(max_length=32, blank=True)
    carrier = models.ForeignKey(
        Carrier, null=True, blank=True, on_delete=models.SET_NULL, related_name="vehicles"
    )
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_vehicle"
        ordering = ["plate_no"]
        verbose_name = "车辆"
        verbose_name_plural = "车辆"

    def __str__(self) -> str:
        return self.plate_no


class Driver(BaseModel, SoftDeleteModel):
    name = models.CharField(max_length=64)
    phone = models.CharField(max_length=32, blank=True, db_index=True)
    id_no = models.CharField(max_length=32, blank=True)
    license_no = models.CharField(max_length=32, blank=True)
    carrier = models.ForeignKey(
        Carrier, null=True, blank=True, on_delete=models.SET_NULL, related_name="drivers"
    )
    is_active = models.BooleanField(default=True)

    class Meta:
        db_table = "md_driver"
        ordering = ["name"]
        verbose_name = "司机"
        verbose_name_plural = "司机"

    def __str__(self) -> str:
        return self.name
