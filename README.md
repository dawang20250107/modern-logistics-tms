# 现代化物流 TMS 平台

现代物流**控制塔 + 运输执行引擎 + 开放 API 平台 + AI Agent 工作台**。
强化 Django 架构，面向高并发（十万级在线 / 万级+ QPS）、强 AI、强扩展性。

- `backend/` — Django 5.2 LTS + DRF，ASGI 异步，模块化应用，UUIDv7 主键，统一响应封装。
- `frontend/` — React + TypeScript + Vite 控制塔（TanStack Query / Table）。
- `deploy/` — Docker Compose 编排、Dockerfile、压测、（后期）腾讯云 Helm/K8s。
- `docs/` — [交付方案](docs/delivery-plan.md)、架构、OpenAPI 契约。

技术栈与架构详见 **[docs/delivery-plan.md](docs/delivery-plan.md)**，部署详见 **[docs/deployment.md](docs/deployment.md)**。

## 功能模块（M1–M7 已交付）

- **平台底座**：JWT 鉴权、组织树 + RBAC 数据权限、对外 API-Key/HMAC 验签（防重放）、幂等键、限流、统一响应信封、结构化日志、Prometheus 指标、健康/就绪探针。
- **运单执行**：订单/运单/主数据、状态机流转（事件留痕）、运单工作台、派车、费用汇总、ETA、**拆单/合单**（货量分配/汇总 + 血缘留痕）。
- **控制塔与实时**：KPI 态势、轨迹**队列削峰 + Celery 批量落库**、ETA/回单定时扫描预警、**SSE 实时事件流**。
- **异常与回单**：异常闭环（分级/指派/处理/关闭 + 费用责任）、回单上传 + **可插拔 OCR**。
- **费用与开放平台**：费用字典、报价规则、应收/应付自动归集、费用/付款接口、**Webhook 网关（HMAC + 重试）**。
- **AI 工作台**：**LangGraph ReAct Agent**（DeepSeek 作为 OpenAI 兼容 LLM）自动编排工具注册表（ETA风险/回单/费用风控/调度建议/异常分析/客服话术），**Postgres checkpointer** 持久化多轮对话状态，**SSE 流式**逐段输出并对接控制塔实时事件流；证据链 + **人工确认闭环**，AI 只建议不自动执行高风险动作。底层工具仍保留 REST 端点向后兼容。
- **智能调度/排线**：可用运力匹配 + **装载适配评分**（紧凑装载优先）+ **多承运商比价**（价低者得）+ 批量**贪心排线**；调度只产出建议/计划，落地仍走人工派车。
- **可扩展 Agent**：工具支持 **per-tool 参数 schema**（接入参数各异的大量 API 无需改图）、**风险分级闸门**（低风险自动执行 / 高风险落建议待人工确认）、以及 **MCP 接入**（`AGENT_MCP_SERVERS` 配置外部 MCP server，工具与内置工具一起绑定进同一张图）。
- **车联网监控（GPS/IoT）**：车载终端建模（GPS/北斗/温度/油耗/ETC/ADAS/DSM），设备上报**队列削峰 + Celery 批量落库**，**车辆实时状态**（在线/离线 + 最新位置）驱动实时定位视图，**统一报警中心**（超速/温度/油量/疲劳/离线…规则引擎 + 去重 + 实时推送 SSE），报警可喂给 LangGraph Agent 归因。
- **电子围栏与轨迹回放**：圆形/多边形**电子围栏**（仓库/线路/限行）+ 进出跳变检测告警；**轨迹回放**（历史轨迹点 + **停留点分析** + 超速段标记）；几何计算（球面距离/点在多边形/点到折线）为纯函数，便于复用与单测。
- **线路管理与偏航**：`Route` 线路（规划路径 + 偏航走廊），运单可绑定规划线路，落库时**点到折线**实时判定**偏航告警**；车辆/司机**证件维保**（年检/保险/维保/驾照/从业资格到期）+ 到期预警接口。
- **IoT 终端接入网关**：**JT/T 808-2013** 位置汇报(0x0200)解析与构帧（纯函数 + 模拟器）、**MQTT 网关**（`mqtt_gateway` 管理命令订阅 broker），终端上报归一化后入现有削峰队列，复用批量落库 + 报警链路。
- **工程化**：Docker 编排、生产 Nginx+gunicorn 编排、GitHub Actions CI、drf-spectacular OpenAPI、k6 压测脚手架。

## 本地一键运行（Docker）

> 运行时固定容器内 Python 3.13，宿主机无需装 Python/PG/Redis，只需 Docker。

```powershell
# 1) 准备环境变量（可选，缺省值已能跑）
copy deploy\.env.example deploy\.env

# 2) 起全栈：Postgres + Redis + 后端(ASGI) + Celery worker + beat
docker compose -f deploy/docker-compose.yml up -d --build

# 3) 创建超级管理员
docker compose -f deploy/docker-compose.yml exec backend python manage.py createsuperuser

# 4) 验证
#   健康/就绪探针
curl http://127.0.0.1:8000/healthz
curl http://127.0.0.1:8000/readyz
#   接口文档（Swagger UI）
#   http://127.0.0.1:8000/api/docs
#   Django Admin
#   http://127.0.0.1:8000/admin/
#   Prometheus 指标
#   http://127.0.0.1:8000/metrics
```

前端开发：

```powershell
cd frontend
npm install
npm run dev   # http://127.0.0.1:5173
```

生产预演（Nginx 网关 + gunicorn，端口 80）：

```powershell
copy deploy\.env.example deploy\.env.prod   # 改强密钥/域名/口令
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build
# http://127.0.0.1/  ·  http://127.0.0.1/api/docs
```

种子演示数据与账号：

```powershell
docker compose -f deploy/docker-compose.yml exec backend python manage.py seed_demo
# admin / Admin12345!（超管）· dispatcher / Dispatch123!（调度·上海网点）· viewer / Viewer123!（只读）
```

## 鉴权

内部用户用 JWT：

```text
POST /api/v1/auth/token        # 登录，返回 access / refresh
POST /api/v1/auth/token/refresh
GET  /api/v1/auth/me           # 当前用户
```

外部系统（财务/OA/ERP/司机端）用 API-Key + HMAC 签名（见 docs）。

## AI / Agent

DeepSeek V4（OpenAI 兼容）。AI 只识别、推荐、解释、预警、草拟，**不自动执行**审核/付款/作废等高风险动作。

```text
GET  /api/v1/ai/deepseek/status
POST /api/v1/ai/deepseek/chat
GET  /api/v1/agent/tools
POST /api/v1/agent/tools/execute
```

环境变量：`DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` / `DEEPSEEK_MODEL`（见 `deploy/.env.example`）。
