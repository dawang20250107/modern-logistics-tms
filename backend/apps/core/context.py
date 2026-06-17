import contextvars

# 贯穿请求生命周期的 RequestID，供日志与审计读取
request_id_var: contextvars.ContextVar[str] = contextvars.ContextVar("request_id", default="-")
