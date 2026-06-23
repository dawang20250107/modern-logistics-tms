"""外部接入状态汇总：哪些通道已配置（live）、哪些为预留（reserved）。"""

from . import feishu, wechat
from .ymm import _configured as ymm_configured


def integration_status() -> dict:
    return {
        "integrations": [
            {
                "key": "ymm", "name": "运满满调车比价",
                "state": "live" if ymm_configured() else "fallback",
                "note": "已实现；未配置凭证时返回离线参考价。",
            },
            {
                "key": "feishu", "name": "飞书 Bot 卡片 / 多维表格",
                "state": "live" if feishu.configured() else "reserved",
                "note": "卡片构造可用；推送与多维表同步预留，配置 FEISHU_APP_ID/SECRET 后启用。",
            },
            {
                "key": "wechat", "name": "微信接入",
                "state": "live" if wechat.configured() else "reserved",
                "note": "企业微信/个人微信自动化预留，配置 WECHAT_* 后启用。",
            },
        ],
    }
