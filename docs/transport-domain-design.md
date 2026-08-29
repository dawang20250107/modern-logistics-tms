# 运输域建模设计（订单 · 运单 · 资源 · 费用 · 上下游）

> 目标：把"订单全要素 + 多资源 + 点位时间自动化 + 费用构成 + 上下游结算"统一建模，
> 支撑复杂干线/支线/接力运输与双向结算。分阶段、每阶段独立 PR + 迁移 + 测试。

## 1. 核心理念：商务意图 ≠ 运输执行

物流系统的根本张力是**客户要什么**与**我们怎么拉**的分离，分两层建模：

| 层 | 模型 | 视角 | 资金方向 | 对接 |
|---|---|---|---|---|
| 订单层 | `Order` | 商务意图：多点位、货物、报价、时效 | 应收 | 上游：客户 / 发货人 / 收货人 |
| 运单层 | `Waybill` | 运输执行：一次牵引车+司机的运输作业 | 应付 | 下游：承运商 / 司机 / 自有成本 |

一个订单可拆成**多段运单**（干线 + 支线 / 接力换车），由 `Waybill.parent / children` 表达。

## 2. 关键差距与设计

| 需求 | 现状 | 设计 |
|---|---|---|
| 订单号 / 创建·修改时间 | ✅ `order_no` + `created_at/updated_at` | 保持 |
| 订单修改 | ✅ 可改 + `OrderEvent` | 增字段级 diff 快照（审计） |
| 提货 / 发车时间 | ⚠️ 仅计划时间 + 事件日志 | 关键里程碑**实际时间落列** |
| 各点位到达时间自动化 | ❌ 运单无点位 | `WaybillStop` + GPS 围栏自动盖戳 |
| 干线线路名 / 点位地址 | ✅ `Route` / `OrderStop.address` | 点位流转到执行层 |
| 司机 + 电话 / 车牌 | ✅ `Driver.phone` / `Vehicle.plate_no` | 保持 |
| **挂车车牌** | ❌ | 牵引车 / 挂车分类 + `Waybill.trailer` |
| **多个司机同行** | ❌ 单 `driver` FK | `WaybillDriver` 分配表（主驾/副驾/接力） |
| **司机关系（员工/外调）** | ❌ | `Driver.employment_type` 驱动结算路径 |
| 承运费用构成 | ⚠️ `ExpenseRecord` 灵活无结构 | 结构化运费构成 + 收款方 |
| 上下游 | ⚠️ Customer / Carrier | 发货人/收货人 + 双向结算闭环 |

## 3. 资源建模（阶段一）

### 3.1 车辆：牵引车 / 挂车分离
- `Vehicle.vehicle_class` ∈ {`tractor` 牵引车, `trailer` 挂车, `rigid` 单体车}。挂车有独立道路运输证/年检，作一等公民。
- `Waybill.vehicle`（牵引车）+ `Waybill.trailer`（挂车 FK）。同一牵引车不同运段可换挂。

### 3.2 司机：多司机 + 雇佣关系
- `WaybillDriver(waybill, driver, role, note)`，`role` ∈ {`main` 主驾, `co` 副驾, `relay` 接力}。解决多司机同行。
  保留 `Waybill.driver` 作主驾，兼容旧数据。
- `Driver.employment_type` ∈ {`employee` 自有员工, `outsourced` 外协外调, `carrier_driver` 承运商司机, `temp` 临时}。
  **决定结算**：员工→薪酬/油卡；外调/承运商→应付运费。

## 4. 点位到达时间自动化（阶段二）

- `WaybillStop`：从订单点位拷贝进执行层，字段：`seq, stop_type, address, consignor/consignee, planned_eta, actual_arrival_at, actual_depart_at, arrival_source`。
- 自动化 = **地理围栏**：`TrackingPoint` 进入点位半径 → 自动盖 `actual_arrival_at` 并推进状态；离开 → `actual_depart_at`。复用 `TrackingPoint` 与 `Route.corridor_m`。
- 关键里程碑（装车/发车/到达/签收）实际时间从事件日志**物化落列**到 `Waybill`，便于 SLA 查询。

## 5. 费用构成与上下游（阶段三）

### 5.1 结构化运费构成
- **应收（客户）**：运费 + 附加（等候/装卸/保险/超区）。
- **应付（承运商/司机）**：运费 + 油卡 + 过路费 + 装卸费 + 押车费 + 信息费 + 回单费 − 异常扣款。
- `ExpenseRecord` 增 `payee_type / payee_ref`（付给：承运商/司机/油卡商）；按 `dispatch_type` 给默认费用模板。

### 5.2 上下游
- 上游应收：Customer → 点位发货人(consignor)/收货人(consignee)。
- 下游应付：Carrier / 外协 Driver / 自有成本。
- 双向结算：复用 `Statement`（对账），补外协司机应付。

## 6. 实施阶段

1. ✅ **阶段一 · 资源建模**：挂车字段 + `WaybillDriver` 多司机 + `Driver.employment_type`。
2. ✅ **阶段二 · 点位时间自动化**：`WaybillStop` + GPS 围栏自动盖戳 + 里程碑落列。
3. ✅ **阶段三 · 费用构成与上下游**：结构化运费科目 + 收款方 + 按收款方归集。
4. ✅ **阶段四 · 订单修改版本与审计**：编辑落字段级 diff 快照（改了哪个字段、从什么到什么）。

每阶段独立 PR、独立迁移、独立测试，保证可回滚、可灰度。

## 运单的三种「结束」：取消 / 作废 / 中止

这三个不是同义词，选错会让统计和费用都跟着错。

| | 什么时候用 | 车走了没 | 统计里怎么算 |
|---|---|---|---|
| **cancelled 已取消** | 业务被取消 | 没走 | 计入合作单量，不计入准班率 |
| **voided 已作废** | 当它没发生过；拆单/合单后的原单也用它 | 没走 | **整个排除**（承运商统计、车辆当前运单都不算） |
| **aborted 已中止** | 走了但送不到了：事故、客户中途取消、装错货、车辆故障 | **走了** | 计入合作单量，不计入准班率；车和人立即释放 |

### 为什么要单独有「中止」

原先 `departed` / `in_transit` 没有任何出口。想终止一张已发车的运单，
系统允许的唯一路径是：

```
departed → in_transit → arrived → rejected → cancelled
```

也就是**必须先记一次没有发生过的到达**。而 `arrived` 会写 `arrived_at` 里程碑，
准班率又正是按 `arrived_at` 取样的——实测走完这条路径后，
那张根本没送到的单进了准班率的分子分母，该承运商准班率 100%。

套用 `voided` 也不对：作废的语义是"当它没发生过"，而车确实开出去过、
人和车确实被占用过、多半还产生了空驶费。抹掉这些是在抹掉事实。

### 规则

- **入口**：`departed` / `in_transit` → `aborted`。车还没走的状态用取消或作废，
  不开中止——三个意思相近的动作会让操作员犹豫。
- **必须填原因**。中止后面必然跟着费用争议（空驶费怎么算、半程运费给不给）
  和责任认定，那时候唯一能还原现场的就是这条原因。不填直接 400。
- **出口只有 `settled`**。中止基本都有钱要算，强制过一次结算；
  确实没有费用时，结算 0 元也是一次明确的确认。让一趟出过车的运输
  悄悄消失才是坏设计。
- **不写任何里程碑**。`aborted` 不在 `milestoneField` 里，所以不会产生假到达。
- **不进送达统计**。`aborted` 不在 `wbstatus.Delivered` 里。
- **立即释放车和人**。占用判定（`busyStatuses`）是白名单，`aborted` 不在其中。

### 状态词表只有一份

运单状态的中文标签此前在后端有 **7 份**拷贝（5 个 Go map + 2 段写死在 SQL 里的
CASE），前端还有第 8 份。加一个状态要改 8 处，漏掉任何一处的表现是
「同一个状态在这个页面显示中文、在那个接口显示英文码」——不报错，
只是看起来像没翻译。

而且它们**本来就已经漂了**：`partially_signed` 和 `rejected` 后端有、
前端没有，这两种运单在界面上一直显示的是原始英文。

现在后端收成 `internal/wbstatus.Label` 一处（SQL 里的 CASE 由
`wbstatus.LabelCaseSQL` 生成）。跨语言那道边界收不掉，改为用
`TestStatusLabelsMatchFrontend` 逐键比对前端的 `STATUS_LABEL`，两边不一致就红。
