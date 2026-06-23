# 外部接入层（integrations）

统一收口对外部系统的接入。已实现的走真实调用，未实现的为**预留接口**——
接口签名、卡片结构、回退逻辑均已就位，配置凭证后替换实现体即可启用，业务侧无需改动。

接入状态可通过 `GET /api/v1/integrations/status` 查询。

## 1. 运满满 / 满帮（已实现）

调车运费比价。`apps/integrations/ymm.py`

| 环境变量 | 说明 |
|---|---|
| `YMM_BASE_URL` | 默认 `https://qa-open.ymm56.com` |
| `YMM_APP_KEY` / `YMM_APP_SECRET` / `YMM_ACCESS_TOKEN` | 开放平台凭证 |
| `YMM_TIMEOUT_SECONDS` | 请求超时（默认 8s）|

- 配置齐全 → HMAC-SHA256 签名请求 `/apis/openapi/workbench`，解析比价区间。
- 未配置或不可达 → 离线参考价（`source=offline`），不阻断派单。
- 已接入派单建议 `GET /orders/{id}/dispatch-suggestion`（字段 `ymm_quote`）与 `GET /orders/{id}/ymm-quote`。

## 2. 飞书 Bot（预留）

`apps/integrations/feishu.py`。四类卡片构造器已可用（真实 interactive card 结构），
推送与多维表格双向同步为预留。

| 卡片 | 构造器 | 触发场景 |
|---|---|---|
| ① 新增车需求 | `new_demand_card(order)` | AI 确认需求完整后推调度员 |
| ② 调度结果提交 | `dispatch_result_card(order, ...)` | 调度员填写后返回 |
| ③ 异常预警 | `exception_alert_card(exc)` | 司机未确认/未注册、在途异常 |
| ④ 转人工请求 | `transfer_human_card(...)` | AI 无法处理时推客服主管 |

启用：配置 `FEISHU_APP_ID` / `FEISHU_APP_SECRET`（多维表另需 `FEISHU_BITABLE_APP_TOKEN`），
在 `push_card` / `sync_to_bitable` 内实现真实调用。

## 3. 微信接入（预留）

`apps/integrations/wechat.py`。企业微信 API / 个人微信自动化。

- `receive_group_message`（群消息→AI 建单入口）、`send_contract_to_driver`（合同下发）、
  `add_driver_wechat`（加司机微信）、`notify_customer`（状态通知）。
- 启用：配置 `WECHAT_PROVIDER`（work/personal）、`WECHAT_CORP_ID` / `WECHAT_CORP_SECRET`。
