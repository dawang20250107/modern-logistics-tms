"""微信接入（预留）：企业微信API / 个人微信自动化。

当前为占位实现——不发起真实网络调用，返回 reserved，供合同发送、加司机微信等流程挂载。
接入真实通道后替换函数体即可，业务侧调用方无需改动。
"""

import logging

logger = logging.getLogger("integrations.wechat")

RESERVED = {"status": "reserved", "channel": "wechat"}


def send_contract_to_driver(contract) -> dict:
    """向司机微信下发承运合同（预留）。"""
    logger.info("wechat.send_contract_to_driver reserved: %s", getattr(contract, "contract_no", ""))
    return {**RESERVED, "action": "send_contract", "contract_no": getattr(contract, "contract_no", "")}


def add_driver_wechat(driver) -> dict:
    """自动添加司机微信（预留）。"""
    logger.info("wechat.add_driver_wechat reserved: %s", getattr(driver, "phone", ""))
    return {**RESERVED, "action": "add_driver"}


def notify_customer(order, message: str) -> dict:
    """向客户微信发送状态通知（预留）。"""
    logger.info("wechat.notify_customer reserved: %s", message[:40])
    return {**RESERVED, "action": "notify_customer"}
