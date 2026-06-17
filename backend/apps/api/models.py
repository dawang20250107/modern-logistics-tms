from django.db import models


class Customer(models.Model):
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    is_active = models.BooleanField(default=True)

    def __str__(self):
        return f"{self.code} {self.name}"


class Carrier(models.Model):
    code = models.CharField(max_length=32, unique=True)
    name = models.CharField(max_length=120)
    is_active = models.BooleanField(default=True)

    def __str__(self):
        return f"{self.code} {self.name}"


class Vehicle(models.Model):
    plate_no = models.CharField(max_length=32, unique=True)
    vehicle_type = models.CharField(max_length=64, blank=True)
    carrier = models.ForeignKey(Carrier, null=True, blank=True, on_delete=models.SET_NULL, related_name="vehicles")

    def __str__(self):
        return self.plate_no


class Driver(models.Model):
    name = models.CharField(max_length=64)
    phone = models.CharField(max_length=32, blank=True)
    carrier = models.ForeignKey(Carrier, null=True, blank=True, on_delete=models.SET_NULL, related_name="drivers")

    def __str__(self):
        return self.name


class Waybill(models.Model):
    STATUS_PENDING_DISPATCH = "pending_dispatch"
    STATUS_IN_TRANSIT = "in_transit"
    STATUS_DELIVERED = "delivered"

    RISK_HIGH = "high"
    RISK_MEDIUM = "medium"
    RISK_LOW = "low"
    RISK_NONE = "none"

    waybill_no = models.CharField(max_length=40, unique=True)
    customer = models.ForeignKey(Customer, null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills")
    carrier = models.ForeignKey(Carrier, null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills")
    vehicle = models.ForeignKey(Vehicle, null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills")
    driver = models.ForeignKey(Driver, null=True, blank=True, on_delete=models.SET_NULL, related_name="waybills")

    route_name = models.CharField(max_length=160)
    origin = models.CharField(max_length=80, blank=True)
    destination = models.CharField(max_length=80, blank=True)
    status = models.CharField(max_length=32, default=STATUS_PENDING_DISPATCH)
    dispatch_status = models.CharField(max_length=32, default="pending_accept")
    risk_level = models.CharField(max_length=16, default=RISK_NONE)
    receipt_status = models.CharField(max_length=32, default="not_due")
    eta_drift_minutes = models.IntegerField(default=0)

    cargo_quantity = models.IntegerField(default=0)
    cargo_weight_ton = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    cargo_volume_cbm = models.DecimalField(max_digits=10, decimal_places=2, default=0)
    planned_arrival = models.DateTimeField(null=True, blank=True)
    estimated_arrival = models.DateTimeField(null=True, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [
            models.Index(fields=["status", "risk_level"]),
            models.Index(fields=["receipt_status"]),
        ]
        ordering = ["-created_at"]

    def __str__(self):
        return self.waybill_no


class WaybillEvent(models.Model):
    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="events")
    event_type = models.CharField(max_length=64)
    event_time = models.DateTimeField()
    resource = models.CharField(max_length=80, blank=True)
    payload = models.JSONField(default=dict, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [models.Index(fields=["event_type", "event_time"])]
        ordering = ["event_time"]

    def __str__(self):
        return f"{self.waybill_id} {self.event_type}"


class TrackingPoint(models.Model):
    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="tracking_points")
    lng = models.DecimalField(max_digits=10, decimal_places=6)
    lat = models.DecimalField(max_digits=10, decimal_places=6)
    speed_kmh = models.DecimalField(max_digits=8, decimal_places=2, default=0)
    reported_at = models.DateTimeField()
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [models.Index(fields=["reported_at"])]
        ordering = ["reported_at"]


class ExpenseRecord(models.Model):
    DIRECTION_RECEIVABLE = "receivable"
    DIRECTION_PAYABLE = "payable"
    DIRECTION_EXTERNAL = "external"

    waybill = models.ForeignKey(Waybill, on_delete=models.CASCADE, related_name="expenses")
    direction = models.CharField(max_length=16)
    expense_item_code = models.CharField(max_length=64)
    amount = models.DecimalField(max_digits=12, decimal_places=2)
    risk_status = models.CharField(max_length=32, default="normal")
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [models.Index(fields=["direction", "risk_status"])]


class ExceptionRecord(models.Model):
    waybill = models.ForeignKey(Waybill, null=True, blank=True, on_delete=models.SET_NULL, related_name="exceptions")
    exception_type = models.CharField(max_length=64)
    description = models.TextField(blank=True)
    status = models.CharField(max_length=32, default="pending_handle")
    responsibility_party = models.CharField(max_length=80, blank=True)
    amount = models.DecimalField(max_digits=12, decimal_places=2, default=0)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [models.Index(fields=["exception_type", "status"])]


class AgentSuggestion(models.Model):
    STATUS_PENDING = "pending"
    STATUS_ACCEPTED = "accepted"
    STATUS_REJECTED = "rejected"

    waybill = models.ForeignKey(Waybill, null=True, blank=True, on_delete=models.CASCADE, related_name="agent_suggestions")
    suggestion_type = models.CharField(max_length=64)
    title = models.CharField(max_length=160)
    body = models.TextField()
    status = models.CharField(max_length=24, default=STATUS_PENDING)
    evidence = models.JSONField(default=dict, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [models.Index(fields=["suggestion_type", "status"])]
        ordering = ["-created_at"]
