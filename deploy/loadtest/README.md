# 压测脚手架

面向「超大并发（十万级在线 / 万级+ QPS）」目标的容量校准入口。

## 前提
- 安装 [k6](https://k6.io/docs/get-started/installation/)。
- 后端在跑（本地：`docker compose -f deploy/docker-compose.yml up -d`）。

## 运行
```bash
k6 run deploy/loadtest/k6-smoke.js
# 指定目标与口令
k6 run -e BASE_URL=http://127.0.0.1:8000 -e TMS_PASS=Admin12345! deploy/loadtest/k6-smoke.js
```

## 说明
- 本地单进程 `uvicorn --reload` 只用于**跑通脚本与发现功能瓶颈**，不代表生产容量。
- 真实容量校准应针对**水平扩展后的部署**（多 backend 副本 + Postgres 读副本 + Redis），
  并补充写热点场景：`POST /api/v1/tracking/points` 批量轨迹上报（验证队列削峰链路）。
- 阈值（可按 SLO 调整）：错误率 < 1%，p95 < 500ms。
