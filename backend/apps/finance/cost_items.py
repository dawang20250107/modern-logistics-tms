"""运费构成标准科目与收款方：承运成本的结构化拆解（运费/油卡/过路/装卸/押车/信息/回单/扣款）。

供运单费用录入与对账上下游归集使用。前后端共用同一套编码。
"""

# 应付（承运成本）科目
COST_ITEMS = {
    "TRANSPORT_COST": "运费",
    "FUEL_CARD": "油卡",
    "TOLL": "过路费",
    "LOADING": "装卸费",
    "DETENTION": "押车费",
    "INFO_FEE": "信息费",
    "RECEIPT_FEE": "回单费",
    "DEDUCTION": "扣款",
    "EXCEPTION_COST": "异常费用",
    "OTHER_COST": "其他成本",
}

# 应收（客户）科目
INCOME_ITEMS = {
    "TRANSPORT_INCOME": "运费收入",
    "SURCHARGE": "附加费",
    "INSURANCE": "保险费",
    "WAITING_FEE": "等候费",
    "OTHER_INCOME": "其他收入",
}

# 收款/付款方类型（上下游）
PAYEE_CARRIER = "carrier"
PAYEE_DRIVER = "driver"
PAYEE_FUEL_CARD = "fuel_card"
PAYEE_CUSTOMER = "customer"
PAYEE_OTHER = "other"
PAYEE_LABELS = {
    PAYEE_CARRIER: "承运商",
    PAYEE_DRIVER: "司机",
    PAYEE_FUEL_CARD: "油卡商",
    PAYEE_CUSTOMER: "客户",
    PAYEE_OTHER: "其他",
}

ALL_ITEM_LABELS = {**COST_ITEMS, **INCOME_ITEMS}


def item_label(code: str) -> str:
    return ALL_ITEM_LABELS.get(code, code)


def payee_label(payee_type: str) -> str:
    return PAYEE_LABELS.get(payee_type, payee_type or "")
