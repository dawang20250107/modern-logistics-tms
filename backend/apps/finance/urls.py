from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import (
    AgingView,
    ExpenseItemViewSet,
    ExpenseRecordViewSet,
    FinancialDashboardMetricsView,
    PaymentRequestViewSet,
    PaymentResultView,
    PricingRuleViewSet,
    ReimbursementViewSet,
    StatementOverviewView,
    StatementViewSet,
    WebhookDeliveryViewSet,
    WebhookViewSet,
)

router = DefaultRouter(trailing_slash=False)
router.register("expense-items", ExpenseItemViewSet, basename="expense-item")
router.register("expense-records", ExpenseRecordViewSet, basename="expense-record")
router.register("payment-requests", PaymentRequestViewSet, basename="payment-request")
router.register("pricing-rules", PricingRuleViewSet, basename="pricing-rule")
router.register("webhooks", WebhookViewSet, basename="webhook")
router.register("webhook-deliveries", WebhookDeliveryViewSet, basename="webhook-delivery")
router.register("statements", StatementViewSet, basename="statement")
router.register("reimbursements", ReimbursementViewSet, basename="reimbursement")

urlpatterns = [
    *router.urls,
    path("payment-results", PaymentResultView.as_view(), name="payment-results"),
    path("aging", AgingView.as_view(), name="aging"),
    path("statement-overview", StatementOverviewView.as_view(), name="statement-overview"),
    path("dashboard-metrics", FinancialDashboardMetricsView.as_view(), name="dashboard-metrics"),
]
