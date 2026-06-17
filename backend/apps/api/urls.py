from django.urls import path

from . import views

urlpatterns = [
    path("health", views.health),
    path("waybills", views.waybills),
    path("waybills/<str:waybill_no>", views.waybill_detail),
    path("waybills/<str:waybill_no>/dispatch", views.dispatch_waybill),
    path("waybills/<str:waybill_no>/events", views.add_waybill_event),
    path("waybills/<str:waybill_no>/costs", views.waybill_costs),
    path("waybills/<str:waybill_no>/eta", views.eta),
    path("tracking/points", views.tracking_points),
    path("exceptions", views.create_exception),
    path("finance/expense-records", views.expense_records),
    path("finance/payment-requests", views.payment_requests),
    path("finance/payment-results", views.payment_results),
    path("ai/query-waybill", views.ai_query_waybill),
    path("ai/deepseek/status", views.deepseek_status),
    path("ai/deepseek/chat", views.deepseek_chat),
    path("agent/tools", views.agent_tools),
    path("agent/tools/execute", views.agent_tool_execute),
]
