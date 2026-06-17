# 现代化物流 TMS — 交付方案

生成日期：2026-06-04 · 状态：执行中（首期：生产级工程底座）

## 0. 已确认的方向（来自需求方拍板）

| 维度 | 决策 | 含义 |
|---|---|---|
| 架构 | **强化 Django**（不换语言） | ASGI 异步 + PostgreSQL + Redis + Celery + 无状态水平扩展 |
| 首期 | **生产级工程底座** | 鉴权/组织/RBAC、网关/限流、Postgres/Redis/Celery、容器化、CI、可观测性 |
| 部署 | **本地先行 → 腾讯云移植** | 本地 Docker Compose 跑通，全程保持云可移植（CVM/TKE 平滑迁移） |
| 量级 | **超大（十万级在线 / 万级+ QPS）** | 重缓存、读写分离、写热点队列削峰、预留分库分表 |
| AI | DeepSeek V4（OpenAI 兼容） | 工具编排 + 证据链 + 人工确认，AI 只建议不自动执行高风险动作 |
| 产品形态 | 控制塔 + 执行引擎 + 开放 API + AI 工作台 | 不复刻传统 TMS；砍掉报销 UI，费用走开放接口 |

> 工程张力说明：纯 Django 不靠框架硬扛万级+ QPS，而是靠架构——读多缓存、写热点（轨迹上报）走消息队列异步落库、无状态实例水平扩展、关键表预留分片键。详见 §4。

## 1. 总体架构（目标态）

```
                         ┌────────────── 客户端 ──────────────┐
                         │  React 控制塔 / 司机端 / 客户门户     │
                         └───────────────┬────────────────────┘
                                         │ HTTPS / JWT / API-Key+HMAC
                  ┌──────────────────────▼──────────────────────┐
                  │   反向代理 / API 网关 (Nginx → 后期 TKE Ingress) │
                  │   TLS · 限流 · 路由 · 静态资源                 │
                  └──────────────────────┬──────────────────────┘
        ┌──────────────────┬─────────────┴───────────┬───────────────────┐
        ▼                  ▼                          ▼                   ▼
  Django ASGI(N实例)   写热点采集端点            Celery Worker(N)      AI Service(同进程/可拆)
  业务 REST API        轨迹/事件高并发写          OCR/轨迹分析/预警       DeepSeek 编排/RAG
        │                  │                          │                   │
        │            Redis Stream/队列 ───────────────┘                   │
        ▼                  ▼                                              ▼
  PostgreSQL(主+读副本, 预留分片)   Redis(缓存/锁/限流/幂等/会话)      对象存储(回单/凭证)
        ▲                                                                 ▲
        └───────────────── Webhook 网关 → 外部 ERP/财务/OA ────────────────┘

  可观测性: Prometheus 指标 + 结构化日志 + RequestID 链路（后期接 OTel/APM）
```

## 2. 技术栈（已锁定）

| 层 | 选型 | 理由 |
|---|---|---|
| 运行时 | Python 3.13（容器内固定） | 规避宿主机 3.14 的 C 扩展兼容风险 |
| 框架 | Django 5.2 LTS + DRF 3.16 | 企业核心系统稳定性优先，支持期长 |
| 鉴权 | SimpleJWT（内部用户）+ API-Key/HMAC（外部系统） | 多端复用，外部接口防重放/可审计 |
| DB | PostgreSQL 16（后期 PostGIS） | JSONB、复杂查询、事务、地理（轨迹）扩展 |
| 缓存/队列/锁 | Redis 7 | 缓存、限流、幂等、会话、Celery broker、Stream 削峰 |
| 异步 | Celery 5 + django-celery-beat | OCR、轨迹分析、预警、报表、AI 后台任务 |
| 服务器 | gunicorn + uvicorn worker（ASGI） | 异步、高并发连接 |
| 文档 | drf-spectacular（代码生成 OpenAPI） | 契约与实现同源 |
| 可观测 | django-prometheus + 结构化日志 + RequestID | 指标/日志/链路 |
| 前端 | React 19 + TS + Vite + TanStack Query/Table | 控制塔、实时、AI Copilot、复杂表格 |
| 编排 | Docker Compose（本地）→ Helm/K8s（腾讯云 TKE） | 一套镜像两处跑 |

## 3. 仓库结构（目标）

```
backend/
  config/            # 多环境 settings、asgi/wsgi、celery、根路由
  apps/
    core/            # 基础模型/UUIDv7、响应封装、异常、分页、中间件、健康检查、限流
    accounts/        # 自定义 User
    iam/             # 组织树、角色、权限、分配、数据权限、JWT 视图、API-Key/HMAC
    audit/           # 审计日志
    masterdata/      # 客户/承运商/车辆/司机
    ops/             # 订单/运单/节点/货物/事件/轨迹/异常
    finance/         # 费用字典/报价规则/应收/应付/费用记录/付款申请
    ai/              # Agent 工具注册表/DeepSeek 客户端/建议落库/审计
deploy/              # docker-compose、Dockerfile、nginx、.env 模板、压测
frontend/            # React 控制塔
docs/                # 交付方案、架构、OpenAPI
.github/workflows/   # CI
```

## 4. 高并发设计要点（十万级 / 万级+ QPS）

- **无状态实例**：Django 进程不存本地状态，会话/锁/限流入 Redis，按 CPU 水平扩展。
- **读路径**：列表/详情多级缓存（Redis + HTTP 缓存头），热点查询缓存，N+1 用 `select_related/prefetch_related` 消除，关键过滤建复合索引。
- **写热点削峰**：轨迹上报、事件流等写密集端点先入 Redis Stream/队列，Celery 批量异步落库，避免直写打爆主库。
- **数据库**：连接池（CONN_MAX_AGE + 后期 PgBouncer）、主从读写分离（Django DB router 预留）、大表（轨迹/事件）按时间或运单分片键预留分表能力，UUIDv7 主键时间有序兼顾分片与索引局部性。
- **幂等与防重放**：外部写接口 `Idempotency-Key` + 时间戳 + HMAC，结果缓存于 Redis。
- **限流**：DRF 用户/匿名限流 + 按 API-Key 配额，均走 Redis 计数。
- **可观测**：QPS/延迟/错误率指标 + 慢查询日志，先有度量再谈优化；上线前 locust/k6 压测校准容量。

## 5. 里程碑

- **M1 生产级工程底座 ✅**：容器编排、平台 core、鉴权与 RBAC + HMAC + 幂等、Celery、可观测、领域模型迁移、AI 底座、前端骨架、CI、本地端到端跑通 + 压测脚手架。
- **M2 运单执行闭环 ✅**：订单→运单→派车→节点/状态机→运单工作台（前后端）。
- **M3 控制塔与轨迹 ✅**：态势看板、轨迹队列削峰 + Celery 批量落库、ETA/回单定时扫描预警、实时事件流（SSE）。
- **M4 异常与回单 ✅**：异常中心闭环（分级/指派/处理/关闭 + 费用责任）、回单上传、可插拔 OCR。
- **M5 费用规则与开放接口 ✅**：费用字典、报价规则、应收/应付自动归集、费用记录/付款申请 API、Webhook 网关（HMAC + 重试）。
- **M6 AI Agent ✅**：ETA风险 / 回单 / 费用风控 / 调度建议 / 异常分析 / 客服话术 共 6 个工具，证据链 + 人工确认，DeepSeek 兜底。
- **M7 部署准备 ✅ / 腾讯云上线（待执行）**：Nginx 网关 + gunicorn 生产编排、前端生产镜像、部署文档（CVM / TKE 两方案）；实际上云需在腾讯云环境执行。

> 状态（截至本次交付）：M1–M7 已开发并本地验证通过（ruff 全过、pytest 13/13、运行态端点全绿）。腾讯云实际上线为环境相关步骤，文档与编排已就绪。

## 6. 本地运行（M1 完成后）

```powershell
# 一键起全栈（首次会构建镜像、拉取 PG/Redis）
docker compose -f deploy/docker-compose.yml up -d --build
# 创建超管
docker compose -f deploy/docker-compose.yml exec backend python manage.py createsuperuser
# 健康检查
curl http://127.0.0.1:8000/healthz
curl http://127.0.0.1:8000/readyz
# 接口文档
# http://127.0.0.1:8000/api/docs
```

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| 纯 Django 扛超大并发 | 架构层削峰+缓存+水平扩展+读写分离，而非单进程硬扛；上线前压测校准 |
| 宿主机 Python 3.14 兼容 | 运行时固定容器内 3.13 |
| AI 误判高风险动作 | AI 只建议，提交/审核/付款/作废由人确认；全链路证据可追溯 |
| 轨迹查询外部成本 | 缓存 + 预算 + 权限 + 审计 |
| 后期腾讯云迁移成本 | 全程 12-Factor、配置走环境变量、镜像与编排解耦、DB/Redis/存储用 URL 注入 |
