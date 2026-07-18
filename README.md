# 智运 TMS · 运输管理平台

面向 B2B 公路货运的**控制塔 + 运输执行引擎 + 开放 API 平台 + AI Agent 工作台**。
强化 Django 架构，面向高并发（十万级在线 / 万级+ QPS）、强 AI、强扩展性；
业务建模贴合中国公路货运实务（运费承担方 / 到付回单付月结 / 代收货款 / 多计费方式 / 派车合规），
安全侧 RBAC 全端点强制 + 数据权限全域生效 + 登录审计，前端为产品级设计系统。

- `backend/` — Django 5.2 LTS + DRF，ASGI 异步，模块化应用，UUIDv7 主键，统一响应封装。
- `frontend/` — React + TypeScript + Vite 控制塔（TanStack Query / Table），Inter 字体 + 统一设计系统。
- `deploy/` — Docker Compose 编排、Dockerfile、压测、（后期）腾讯云 Helm/K8s。
- `docs/` — [交付方案](docs/delivery-plan.md)、架构、OpenAPI 契约。

技术栈与架构详见 **[docs/delivery-plan.md](docs/delivery-plan.md)**，部署详见 **[docs/deployment.md](docs/deployment.md)**。

## 功能模块（M1–M7 已交付）

- **平台底座 / 安全**：JWT 鉴权、组织树 + RBAC；**权限点全端点强制**（组织/员工/角色/AI/看板/车联网等，超管 `["*"]` 放行）、**组织数据权限全域生效**（`org`/`org_sub` 按组织子树过滤运单/订单/异常/回单/财务）、**登录审计 + 失败锁定**（滑动窗口连续失败锁定，锁定期正确密码亦拒）、**AI/LLM 端点限流**（防 token 成本 DoS）；对外 API-Key/HMAC 验签（防重放）、幂等键、限流、统一响应信封、结构化日志、Prometheus 指标、健康/就绪探针。
- **AI 多渠道建单**：统一 `Order` 入口 + 渠道（客服代下/客户自助/小程序/微信群/API）+ 来源类型（个人/企业/政府）、业务类型（整车/零担/快递/冷链）、优先级/结算方式、收发货详址、货值/危险品/温区等企业级字段；**自然语言/微信群消息 → 结构化订单**（DeepSeek 抽取，规则兜底）+ 人工改并存；结构化单号（DD/YD+日期+日序，并发唯一）、批量操作、软删。
- **订单池与调度台**：确认进池（SSE 实时通知）→ **多调度并发认领**（行锁防抢单）→ **AI 派单建议**（系统运力池 + 多承运商比价 + 外部信号）→ 派单（自有单车/车队/三方承运商）转运单。
- **运单执行**：订单/运单/主数据、状态机流转（事件留痕）、运单工作台、派车、费用汇总、**拆单/合单**（货量分配/汇总 + 血缘留痕）。
- **中国货运业务建模**：**运费承担方**（发货方/收货方/第三方）+ **付款方式**（现付/到付/回单付/月结）+ **代收货款 COD**（司机代收 → 回款货主全流程）；**多计费方式**（整车一口价/阶梯重量/按方/按件/按公里/吨公里 + 最低计费量）；客户**授信额度/账期/账单日**。
- **派车合规硬阻断**：证件（年检/保险/驾照/从业资格）到期、**车型/车长匹配**、**准驾资格**（牵引车须 A2）不符则拦截派车，不上违规车。
- **ETA 预测 + 签收环**：基于当前定位 + 剩余里程 + 实测均速的 **ETA 动态预测**与真实**准班率**；签收补齐**整签 / 部分签收 / 拒收 / 货损货差**三态（自动立异常），签收/拒收全程**行级锁 + 事务**防并发双签。
- **控制塔与实时**：KPI 态势、轨迹**队列削峰 + Celery 批量落库**、ETA/回单定时扫描预警、**SSE 实时事件流**。
- **异常与回单**：异常闭环（分级/指派/处理/关闭 + 费用责任）、回单上传 + **可插拔 OCR**（未接真实引擎时**诚实降级为待人工核验，绝不伪造签收人/证件到期**，不覆盖人工录入）、**司机/客户签收回传（e-POD：电子签名 + 一步推进签收 → 触发订单完成）**。
- **订单溯源与通知**：订单全生命周期事件溯源（建单→完成全程留痕 + 时间线）、站内**通知中心**（进池通知调度 / 完成通知客服，未读角标 + SSE 实时响铃）。
- **费用与开放平台**：费用字典、报价规则、应收/应付自动归集、费用/付款接口、**Webhook 网关（HMAC + 重试）**。
- **对账单（ERP 财务）**：按客户(应收)/承运商(应付)在账期内归集费用，**对账单生成 → 明细快照 → 人工确认**，支持对方金额**差异稽核**（diff）。
- **AI 工作台**：**LangGraph ReAct Agent**（DeepSeek 作为 OpenAI 兼容 LLM）自动编排工具注册表（ETA风险/回单/费用风控/调度建议/异常分析/客服话术），**Postgres checkpointer** 持久化多轮对话状态，**SSE 流式**逐段输出并对接控制塔实时事件流；证据链 + **人工确认闭环**，AI 只建议不自动执行高风险动作。底层工具仍保留 REST 端点向后兼容。
- **智能调度/排线**：可用运力匹配 + **装载适配评分**（紧凑装载优先）+ **多承运商比价**（价低者得）+ 批量**贪心排线**；调度只产出建议/计划，落地仍走人工派车。
- **调度指挥中心**：一屏指挥大屏——实时地图 + KPI 摘要（在线运力/待调度/在途/报警）+ 待调度池（逐单 **AI 调度建议**）+ **一键 AI 排线** + 实时报警流（SSE）。
- **可扩展 Agent**：工具支持 **per-tool 参数 schema**（接入参数各异的大量 API 无需改图）、**风险分级闸门**（低风险自动执行 / 高风险落建议待人工确认）、以及 **MCP 接入**（`AGENT_MCP_SERVERS` 配置外部 MCP server，工具与内置工具一起绑定进同一张图）。
- **车联网监控（GPS/IoT）**：车载终端建模（GPS/北斗/温度/油耗/ETC/ADAS/DSM），设备上报**队列削峰 + Celery 批量落库**，**车辆实时状态**（在线/离线 + 最新位置）驱动实时定位视图，**统一报警中心**（超速/温度/油量/疲劳/离线…规则引擎 + 去重 + 实时推送 SSE），报警可喂给 LangGraph Agent 归因。
- **电子围栏与轨迹回放**：圆形/多边形**电子围栏**（仓库/线路/限行）+ 进出跳变检测告警；**轨迹回放**（历史轨迹点 + **停留点分析** + 超速段标记）；几何计算（球面距离/点在多边形/点到折线）为纯函数，便于复用与单测。
- **线路管理与偏航**：`Route` 线路（规划路径 + 偏航走廊），运单可绑定规划线路，落库时**点到折线**实时判定**偏航告警**；车辆/司机**证件维保**（年检/保险/维保/驾照/从业资格到期）+ 到期预警接口。
- **IoT 终端接入网关**：**JT/T 808-2013** 位置汇报(0x0200)解析与构帧（纯函数 + 模拟器）、**MQTT 网关**（`mqtt_gateway` 管理命令订阅 broker），终端上报归一化后入现有削峰队列，复用批量落库 + 报警链路。
- **数据中台 · 指标中台**：统一指标注册表（运单/运力/订单/财务四主题域，口径集中）、指标查询 API（目录/多指标查询/趋势）、**按日物化快照**（趋势加速）、经营看板前端；指标同源供 **AI Agent 直接调用**做经营分析（`analytics.query_metric` 工具）。
- **前端设计系统**：对标顶尖 SaaS 的产品级视觉——克制的 slate 中性色 + 单一 indigo 主色、1px 发丝边框 + 轻量分层阴影、打包 **Inter 可变字体**、数据表等宽数字；统一的 `panel/table/tag/kv/kpi/chip` 组件词汇，导航按功能命名并按权限收敛（含经营看板/指挥中心/AI 助手/数据目录）。
- **工程化**：Docker 编排、生产 Nginx+gunicorn 编排、GitHub Actions CI、drf-spectacular OpenAPI、k6 压测脚手架。

## 生产化打磨（近期深度强化）

在既有 M1–M7 骨架上，按“做深度、交付顶尖产品”的目标逐块打磨（每块独立 PR、测试、CI 绿后合并）：

- **业务纵深**：运费承担方 / 到付回单付月结 / 代收货款 COD；六种计费方式 + 最低计费量；派车合规硬阻断（证件到期 / 车型车长 / 准驾）；ETA 预测引擎 + 真实准班率；签收环补齐（部分签收 / 拒收 / 货损货差）+ 并发防护。
- **安全加固**：RBAC 全端点强制、`/auth/me` 下发权限码 + 前端收敛；登录审计 + 失败锁定；组织数据权限全域生效；AI / 经营看板 / 车联网端点鉴权 + LLM 限流。
- **去伪存真**：回单 / 证件 OCR 由随机数伪造改为诚实降级（待人工核验），杜绝“过期证件被洗白、假签收人误导财务核销”；监控页移除预置假报警，仅渲染真实 SSE 告警；`dispatch` 端点改走状态机、修复多运单订单误判完单。
- **产品级前端**：全站配色 / 字体 / 排版重构，去除演示注释式文案。

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

# 组织中台演示（组织树/部门/用户组/员工含汇报线/服务区划 + 权限点与角色）
docker compose -f deploy/docker-compose.yml exec backend python manage.py seed_org
# 实战全链路演示数据（完成/在途/异常/报销/对账，幂等可 --fresh 重跑）
docker compose -f deploy/docker-compose.yml exec backend python manage.py seed_realistic
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
