# 部署指南（本地预演 → 腾讯云）

## 架构

```
客户端 → Nginx(web 容器, :80, 反代 + 前端静态)
           ├─ /            → 前端 SPA (dist)
           ├─ /api,/admin… → backend (gunicorn + uvicorn worker, :8000)
           └─ /api/v1/stream → backend (SSE, 关闭缓冲)
backend ── PostgreSQL / Redis / 对象存储
worker/beat ── Celery（轨迹削峰落库、ETA/回单扫描、Webhook 投递）
```

## 本地生产预演

```powershell
# 强随机密钥与口令写入 deploy/.env.prod（参考 .env.example）
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build
docker compose -f deploy/docker-compose.prod.yml exec backend python manage.py createsuperuser
# 访问 http://127.0.0.1/ （前端）、http://127.0.0.1/api/docs 、/healthz
```

> 注意：与开发编排（`docker-compose.yml`）端口不同，生产用 80。两者不要同时占用同一 DB 卷。

## 关键环境变量（生产必设）

| 变量 | 说明 |
|---|---|
| `DJANGO_SECRET_KEY` | 强随机 ≥32 字节 |
| `DJANGO_ALLOWED_HOSTS` | 域名/IP，逗号分隔 |
| `DJANGO_CSRF_TRUSTED_ORIGINS` | 前端 https 源 |
| `DJANGO_SSL_REDIRECT` | 启用 TLS 后置 `true` |
| `DATABASE_URL` / `REDIS_URL` | 托管 PG/Redis 时替换为云上地址 |
| `DEEPSEEK_API_KEY` | 启用真实 AI 对话/客服话术 |

## 迁移到腾讯云

### 方案 A：CVM 单机（最快）
1. CVM 安装 Docker + Compose；安全组放行 80/443。
2. 拉取代码，写 `deploy/.env.prod`（强密钥、域名、口令）。
3. 建议改用**托管服务**：TencentDB for PostgreSQL、TencentDB for Redis、COS 对象存储 —— 仅改 `DATABASE_URL` / `REDIS_URL` / 存储配置即可，无需改代码（12-Factor）。
4. `docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build`。
5. 前置 Nginx/CLB 终止 TLS，置 `DJANGO_SSL_REDIRECT=true`、`SECURE_PROXY_SSL_HEADER` 已配置。

### 方案 B：TKE（Kubernetes，面向超大并发）
1. 镜像推送 TCR：`tms-backend:prod`、`tms-frontend:prod`。
2. backend Deployment 多副本（无状态，HPA 按 CPU/QPS 自动扩缩）；worker/beat 各自 Deployment（beat 单副本）。
3. 托管 PostgreSQL（读写分离 + 只读副本）、Redis（集群）、COS。
4. Ingress（CLB）路由 + TLS；`/api/v1/stream` 关闭缓冲、长连接超时。
5. 配置经 ConfigMap/Secret 注入；探针用 `/healthz`(liveness)、`/readyz`(readiness)。
6. 指标 `/metrics` 接入 Prometheus + Grafana；日志 JSON 采集到 CLS。

## 容量与压测

上线前用 `deploy/loadtest/k6-smoke.js` 对**扩缩容后的部署**压测，校准副本数与 DB 规格；
写热点（轨迹上报）走 Redis 队列削峰，验证 worker 消费速率与积压告警。
