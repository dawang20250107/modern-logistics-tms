# Django → Go 迁移手册（绞杀者模式）

## 架构

```
前端 (React, 零改动)
   │  http://<host>:8000
   ▼
Go 网关 (backend-go, chi + pgx)          ← 对外唯一入口
   ├── 已移植域 → 原生处理（直连 PostgreSQL）
   └── 其余全部 → 反向代理 → Django (:8001)   ← 逐域摘除，直至退役
```

两栈共享同一 PostgreSQL 与同一 `DJANGO_SECRET_KEY`：Go 签发 simplejwt 兼容
HS256 token（同 claims：`token_type/exp/iat/jti/user_id`），**两侧互认**，
用户与前端在迁移全程零感知。

## 运行

```bash
# Django 上游（迁移期保留；裸机无 docker 时用 local_standalone 配置）
cd backend  && python3 manage.py runserver 127.0.0.1:8001 --settings=config.settings.local_standalone
# Go 网关（对外入口）
cd backend-go && go run ./cmd/server
# 环境变量（均有开发默认值）：
#   GO_LISTEN_ADDR=:8000  DATABASE_URL=postgres://...  DJANGO_SECRET_KEY=...
#   DJANGO_UPSTREAM=http://127.0.0.1:8001  DJANGO_CORS_ORIGINS=...
```

## 契约铁律（每个域移植前必读）

1. **响应信封** `{success, data, error}` —— 一律走 `httpx.JSON / httpx.Err`。
2. **分页信封** `{items, total, page, page_size, pages}`，`page_size` 上限 200。
3. **Decimal 输出为字符串**（DRF DecimalField 语义），SQL 里 `::text`。
4. **列表四件套**：`search=`（跨列 ILIKE）、`ordering=`（白名单，`-`前缀降序）、
   `filter=<JSON>`（`internal/filters`，与前端 FilterBuilder 对齐）、分页。
5. **数据范围**：`auth.Service.ScopeOrgIDs`（all→nil / org / org_sub 子树 / self）。
6. **日期筛选**时区语义 `AT TIME ZONE 'Asia/Shanghai'`（对齐 Django `__date`）。
7. 宽容解析：非法 filter JSON / 未知字段静默忽略，绝不 500。

## 参考实现

`internal/orders/handler.go` 是标准模板：一条主 SQL（JOIN + LATERAL 聚合）
替代 Django 的 `select_related/prefetch_related`，嵌套明细用 `json_agg`
子查询一次带出，天然无 N+1。移植新域时复制该模式：

1. 从 Django ViewSet 抄 `server_filter_fields` → `filterFields` 映射
2. 从 serializer `Meta.fields` 对齐 SELECT 列与 JSON 键名
3. `ordering_fields` → `orderingCols` 白名单
4. 写完跑 **双栈 diff**（见下）再切路由

## 验证方法（每域必做）

```bash
# 同一请求打双栈，逐字段 diff（首移植 orders 已验证：24 单全字段一致）
curl -s "http://127.0.0.1:8000/api/v1/<res>?..." -H "Authorization: Bearer $TOK" > go.json
curl -s "http://127.0.0.1:8001/api/v1/<res>?..." -H "Authorization: Bearer $TOK" > dj.json
# （比对脚本示例见 git 历史中的验证命令）
```

## 终态目标：纯原生 Go 系统（Django 完全退役）

```
阶段0 网关+认证+订单读        ✅ 完成
阶段1 读路径全量 Go           ◐ 进行中（orders/waybills 读已完成）
      waybills 读 ✅ → masterdata 读 → finance 读/聚合 → workbench/stats → audit
阶段2 写路径                  orders intake/流转/派单 → waybills 状态机/回单/签收
      → finance 核销（事务+行锁）→ masterdata CRUD → iam/org 管理
阶段3 平台域（去 Django 依赖）
      SSE → Go 原生（PG LISTEN/NOTIFY）      媒体文件 → Go 静态服务
      celery 定时任务 → Go cron(robfig/gocron)  telematics MQTT → Go 消费者批量落库
      审计日志中间件 → Go 网关统一记录          admin 后台 → 前端管理页补齐后弃用
阶段4 AI 域 Go 原生
      放弃 langgraph（Python-only），用 LLM HTTP API + 手写 tool-calling 循环
      重写 agent 工作台（Go goroutine 天然适配并行工具调用）
阶段5 收官
      schema 所有权移交：Django migrations 基线化 → goose/atlas 接管
      删除 backend/ 与反向代理，网关变纯应用；部署收敛为单二进制 + PG
```

推进法则：每域「抄契约 → 一条主 SQL → 双栈 diff → 切路由」，任何时刻系统整体可用。

## 已移植

| 域 | 路由 | 状态 |
|---|---|---|
| 认证 | POST /auth/token, /auth/token/refresh; GET /auth/me | ✅ 双栈互认 |
| 订单-读 | GET /orders（筛选/搜索/排序/分页/数据范围）, GET /orders/funnel | ✅ 逐字段 diff 一致；~12× 提速（3.9ms vs 49.1ms/50 行） |
| 运单-读 | GET /waybills（筛选/搜索/排序/分页/权限/数据范围/司机嵌套/应收应付聚合）, GET /waybills/stats | ✅ 20 张逐字段 diff 一致；stats 修正了 Django 的 JOIN 放大重复计数 bug（详见差异清单） |
| 主数据-读 | GET /customers /vehicles /drivers /b2b-partners /carriers + /audit-logs | ✅ 六资源双栈 diff 全一致（carriers 含风控文案/到期预警 SQL 内联）；通用行→JSON 引擎（列别名即键，新资源仅需一份 resourceCfg） |
| 财务-读 | GET /finance/statement-overview + /statements 台账 + /aging 账龄 | ✅ 双栈一致（overview 数值语义 deep-diff；statements 8 张逐字段；Decimal property 以 ::text 保刻度） |
| 订单-写 | POST /orders/intake（规则解析/客户对齐/取号/嵌套写入/审批闸，全事务） | ✅ Go 建单→Django 读回一致；取号跨栈连续 |
| 订单-流转 | POST /orders/{id}/confirm·pool·cancel·claim·release·unassign + /orders/assign 批量分单 | ✅ 全生命周期实测通过；行锁防抢单；进池通知扇出落库；Django timeline 读回事件链完整 |
| 批量派车 | POST /orders/batch-dispatch（批次+N 运单+应付分摊+费用快照+点位拷贝+双事件，单事务） | ✅ 3 单按吨分摊 2:4:6 精确、之和恒等总额；Django 读回运单/批次/费用全对 |
| 运单状态机 | POST /waybills/{no}/transition + /sign + /stop-event（行锁事务：里程碑物化、e-POD 回单落库、订单完成回写、司机累计、点位手动戳） | ✅ pending_dispatch→…→signed 全链实测；非法流转 409；sign 自动 arrived→signed + 回单 confirmed + receipt_status=received；兄弟运单全完成才回写订单 completed（实测生效）；Django 读回详情/回单/订单全对 |
| 详情-读 | GET /orders/{id} + /orders/{id}/timeline + GET /waybills/{no}（stops/timeline/agent_suggestions/next_statuses 全嵌套） | ✅ 5 订单详情+timeline、3 运单详情双栈语义 diff 全一致；404 信封对齐 DRF；静态路由（funnel/stats）与代理子路由（workflow/eta）优先级回归通过 |
| 经营看板 | GET /analytics/dashboard?trends=true（13 指标卡 + 5 趋势，指标中台口径逐条翻译） | ✅ 双栈语义 diff 全一致（含 breakdown 构成/占比分子分母/趋势序列） |
| 工作台 | GET /workbench（通知/异常/客服/调度/财务待办聚合 + 两组 Top5 订单嵌套） | ✅ 计数与嵌套全对齐；唯一差异 dispatchable 系修正 Django 缺陷（见差异清单） |
| 证件预警 | GET /credentials/expiring?days=N（车辆年检/保险/维保 + 司机驾照/从业资格 + 承运资质，severity 分级） | ✅ days=30/90 双栈 diff 全一致（含稳定排序与 summary 计数） |
| 财务大屏 | GET /finance/dashboard-metrics?days=N（营收/成本/毛利按日趋势 + 成本科目构成，读侧） | ✅ days=7/30 双栈 diff 全一致 |
| 组织中台-读 | GET /org/{overview·organizations·organizations/tree·roles·rbac/matrix·service-areas·employees·handovers·login-audit·route-resolve}（列表复用通用引擎，树/矩阵/区划仲裁定制） | ✅ 十端点双栈 diff 全一致（含子树人头累加、覆盖排他+优先级仲裁） |
| 组织中台-写 | POST /org/{organizations·employees·service-areas} 创建 + employees/{id}/{roles·enable·disable·reset-password·handover} + roles/{id}/set-permissions | ✅ Go 写→Django 读回全对；物化路径 path 正确；重置密码 Go 生成 pbkdf2 哈希双栈均可登录；移交事务（下属改挂+部门改派+停用留痕）实测；唯一性/无账号 400 契约对齐 |

## 待移植（按前端依赖频度排序）

1. **orders 写路径**：intake / claim / assign / batch-dispatch / report-exception /
   状态流转 / import / quote / parse-preview（业务规则最重，建议对照 `apps/ops/intake.py`、`order_dispatch.py` 逐函数翻）
2. **waybills**：列表/详情/状态机/回单/费用（表 `ops_waybill*`）
3. **workbench / stats 聚合**：控制塔与工作台的只读聚合（纯 SQL，收益快）
4. **masterdata**：customers/carriers/vehicles/drivers/b2b-partners/lane-prices（模板化 CRUD）
5. **finance**：statements/agings/settle/payments（事务密集，注意核销的行锁）
6. **iam/org**：组织树/员工/RBAC 矩阵（读多写少）
7. **telematics ingest**：MQTT 高频写入——Go 的主场，建议独立 goroutine 池批量落库
8. **SSE /stream/events**：Go 原生 SSE + PG LISTEN/NOTIFY 或 Redis Stream
   （现状 Django SSE 依赖 redis 主机名，本地容器本就不可用——移植时一并解决）
9. AI 域（langgraph）：保留 Django/Python 最久，或独立 Python 微服务

## 尚未对齐的已知差异（限制清单）

- **授权变更的权限缓存**：Django 侧 `effective_permissions` 有 TTL 缓存
  （`iam:perms:<uid>`），Go 写角色分配后不主动清 Django 缓存，靠 TTL 过期兜底；
  Go 自身每请求实时查库无缓存。Django 退役后此差异消失。
- **org 域 CSV 导入/导出、departments/employee-groups/permissions 列表**仍由代理
  提供（前端低频/未用），随收官阶段一并原生化。
- **/workbench 的 dispatchable 修正而非复刻**：Django WorkbenchView 调用
  `OrderSerializer(..., many=True)` 时未传 `context={"request": ...}`，
  `get_dispatchable` 拿不到当前用户恒返回 false。Go 版按真实用户口径计算
  （与 /orders 列表一致）：主调度/认领人看到 true。属 Django 漏传 context 缺陷。
- **datetime 时区表示**：Django 输出 `+08:00`（Asia/Shanghai），Go 输出 UTC
  （`Z`/`+00:00`）——同一时刻的两种 ISO 表示，前端 `new Date()` 解析零感知。
  双栈 diff 均按解析后时间戳比较。
- **点位 seq 重复时的同 seq 内顺序**：Django `ordering=["waybill","seq"]` 无决胜键、
  顺序不确定；Go 补 `ORDER BY seq, id` 决定性排序。集合完全一致。
- **/waybills/{no}/transition 响应精简**：Django 返回整份 WaybillDetailSerializer，
  Go 返回 `{waybill_no, status, next_statuses}`。前端对该响应只做 invalidate 重取、
  不消费内容，故安全；运单详情 GET 移植后可改为复用详情序列化。
- **状态机暂不发 Webhook/SSE**：Django `transition_waybill` 里的 `emit_event`（外部
  Webhook）与 `publish_event`（SSE，依赖 Redis，本地本就不可用）属阶段 3 平台域，
  随 SSE/集成域移植一并补上。事件均已落库 `ops_waybill_event`，不丢数据。

- **/waybills/stats 修正而非复刻**：Django 版在带费用 Sum annotate 的 queryset 上
  `values("status").annotate(Count("id"))`，费用 JOIN 放大导致各状态重复计数
  （各状态之和 43 ≠ 实际 20 张）。Go 版直查 `GROUP BY status`，之和恒等于总数。
  前端状态药丸此前显示的是虚高值，切 Go 后自动恢复真实。

- refresh token 轮换后旧 token 未进服务端黑名单（simplejwt 的 token_blacklist 表未写）；
  测试项目范围可接受，正式化时补 `iam_outstanding/blacklisted` 写入。
- `/auth/me` 的 `avatar_url` 经代理指向 Django `/media/`；媒体文件迁 Go 静态服务后改。
- Django admin (`/admin/`) 持续由代理提供，直至后台管理页面完全前端化。
