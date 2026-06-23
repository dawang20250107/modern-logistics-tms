"""单据号生成：基于 NumberCounter 的原子序列，保证多客服/多进程并发唯一。

订单号 DD + 日期 + 6 位日序号（如 DD20260617000001）：有序、可读、按日复位、唯一。
运单号 YD + 日期 + 6 位日序号。
"""

from django.db import transaction

from .models import NumberCounter


def next_sequence(scope: str) -> int:
    with transaction.atomic():
        counter, _ = NumberCounter.objects.select_for_update().get_or_create(scope=scope)
        counter.value += 1
        counter.save(update_fields=["value"])
        return counter.value


def order_no(now) -> str:
    day = now.strftime("%Y%m%d")
    return f"DD{day}{next_sequence(f'order:{day}'):06d}"


def waybill_no(now) -> str:
    day = now.strftime("%Y%m%d")
    return f"YD{day}{next_sequence(f'waybill:{day}'):06d}"


def contract_no(now) -> str:
    day = now.strftime("%Y%m%d")
    return f"HT{day}{next_sequence(f'contract:{day}'):06d}"
