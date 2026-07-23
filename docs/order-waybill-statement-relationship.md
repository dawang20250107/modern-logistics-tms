# 订单 · 运单 · 对账单 关系模型（含极端复杂场景）

> 本文确立 TMS 三类核心单据 **订单（Order/DD）→ 运单（Waybill/YD）→ 对账单（Statement/ST）** 的数据与业务关系，
> 并逐一说明极端复杂场景下的建模方式、支持程度与已知边界。所有字段/表名均以代码为准（`apps.ops`、`apps.finance`）。

## 1. 三类单据定位

| 单据 | 号段 | 归属方 | 语义 | 关键表 |
|---|---|---|---|---|
| 订单 Order | `DD…` | 客户侧（商务） | 客户的一次委托（要把什么货从 A 运到 B、报价多少） | `ops_order` |
| 运单 Waybill | `YD…` | 承运侧（履约） | 一次实际的运输执行（谁的车、走哪条路、回单签收） | `ops_waybill` |
| 对账单 Statement | `ST…` | 财务侧（结算） | 某对手方在某账期内的应收/应付归集，供确认→核销 | `fin_statement` |

一句话：**订单是"该收谁多少钱"的商务承诺；运单是"实际怎么运、要付谁多少钱"的履约记录；对账单是把一段时间内的应收/应付按对手方归集起来结算的财务单据。**

## 2. 基数关系（Cardinality）

```
                        1        N                     1        N
   masterdata.Customer ───< Order ───────(拆单/合单)>─── Waybill >───┐
                                │ Waybill.order (FK, SET_NULL)        │
                                │ related_name="waybills"             │ Waybill.batch (FK)
                                ▼                                     ▼
                        （客户 AR 应收）                        DispatchBatch（承运商应付分组）
                                                                      │ batch.statement_no（字符串回填）
   Waybill 1 ──< N ExpenseRecord（direction=receivable/payable/external）
                     │ ExpenseRecord.waybill (FK, CASCADE)
                     ▼
   ExpenseRecord 1 ──< N StatementLine ──> 1 Statement
                     StatementLine.expense_record (FK, SET_NULL)   StatementLine.statement (FK, CASCADE)
                     │
   Statement 1 ──< N StatementPayment（收付款核销流水，驱动 draft→confirmed→partial→settled）
```

要点：

- **Order → Waybill：1 : N。** FK 在运单侧 `Waybill.order`（可空 `SET_NULL`，`related_name="waybills"`）。
  一张订单可拆成多张运单；运单还有自引用 `Waybill.parent`（拆单/合单血缘）。
- **N 个 Order → 1 个 Waybill：不支持。** `Waybill.order` 是单值 FK，一张运单只能指向至多一张订单。
  "多单合一车"通过 **批次 DispatchBatch**（商务归集）表达，而非合并成一张运单。
- **Waybill → Statement：无直接外键，全程间接。** 链路是
  `Waybill → ExpenseRecord → StatementLine → Statement`。
  归集口径由 `Statement.direction`（应收/应付）+ `counterparty_type`（客户/承运商）决定：
  - 应收 AR：`ExpenseRecord(direction=receivable)` 按 `waybill.customer` 归集给客户；
  - 应付 AP：`ExpenseRecord(direction=payable)` 按 `waybill.carrier` 或 `waybill.batch` 归集给承运商。
- **一张运单同时喂养两类对账单**：它的应收进客户 AR 单，应付进承运商 AP 单。二者独立。
- **批次 → 对账单**：`generate_statement_for_batch` 按 `waybill__batch_id` 归集该批次全部应付为一张 AP 单，
  并把 `batch.statement_no` 回填（幂等，防重复生成）。

## 3. 费用（ExpenseRecord）——连接运单与对账单的枢纽

`ExpenseRecord` 是三单关系的真正枢纽，一切金额都以它为准（对账不再重算）：

- `direction`：`receivable`（应收，付款方=客户）/ `payable`（应付，收款方=承运商/司机/油卡）/ `external`（平台服务费等）。
- 派单时 `_snapshot_payable` 把**议定应付**固化为一条 `payable` 快照（`price_source=batch/manual/…`）。
- 录单/生成成本时 `generate_costs` 按报价规则生成 `receivable`（客户）与 `payable`（承运商或主副驾拆账）。
- `occurred_at` = 运单建单时间，决定费用落在哪个账期（对账/账龄按此归集）。

## 4. 单据生命周期与状态

- **Order.status**：`draft → pending_confirm → confirmed → pooled → dispatching → converted(已转运单) → completed / cancelled`
- **Waybill.status**：`draft → pending_dispatch → dispatched → loaded → departed → in_transit → arrived → (partially_signed) → signed → delivered → settled / cancelled / voided`
- **Statement.status**：`draft → confirmed → partial(部分结算) → settled`
  - 由 `StatementPayment` 收付款核销累加 `settled_amount` 驱动；`outstanding = total_amount − settled_amount`。

## 5. 极端复杂场景矩阵

| # | 场景 | 如何建模 | 支持度 | 关键字段/说明 |
|---|---|---|---|---|
| A | 一单一运（最常见） | 1 Order → 1 Waybill | ✅ 完全支持 | `dispatch_order` 每次生成一张运单 |
| B | 一单拆多运（分批/多承运商） | 1 Order → N Waybill，`Waybill.parent` 记血缘 | ⚠️ 结构支持、按量拆分未内置 | schema 允许；当前派单每单出一张全量运单，跨承运商按量拆分需补拆分/分摊逻辑 |
| C | 多单一批派同一承运商 | N Order → 1 DispatchBatch → N Waybill → 1 AP 对账单 | ✅ 完全支持 | `batch_dispatch_orders` + `generate_statement_for_batch`；应付按 by_weight/even/manual 分摊 |
| D | 跨客户批次（不同客户拼一车给同一承运商） | 同 C，批次内运单各自 `customer` 不同 | ✅ 支持 | `DispatchBatchDetailSerializer.get_customer_summary` 去重列客户；AR 仍按各自客户归集，AP 归到批次承运商 |
| E | 一运单同时出应收与应付单 | 应收按 `waybill.customer`、应付按 `waybill.carrier` 分别成单 | ✅ 支持 | `ExpenseRecord.direction` + `Statement.direction/counterparty_type` 区分 |
| F | 自营多司机拆账（主驾 60% / 副驾均分 40%） | 一笔应付拆成多条 `payable` ExpenseRecord | ✅ 支持 | `generate_costs` 按 `WaybillDriver.role` 拆分 |
| G | 部分结算 / 分次收付款 | `StatementPayment` 多条 + `settled_amount` 累加 | ✅ 支持 | 状态 confirmed→partial→settled；`outstanding` 属性 |
| H | 跨账期结算（本期账单下期付款） | `Statement.period_*` 与 `StatementPayment.paid_at` 独立 | ✅ 支持（含双开风险，见 §6） | 付款日不受账期约束 |
| I | 异常/扣款进对账 | `ExceptionRecord.amount` + `responsibility_party` | ❌ 未接入对账 | 异常金额目前只在 `waybill_finance_card` 只读展示，不生成负向 StatementLine（见 §6） |
| J | 代收货款 COD | Order/Waybill 的 `cod_amount/cod_status` | ➖ 独立建模 | 与运费分离，不进对账单 |

## 6. 已知边界 / 待加固（供后续迭代）

1. **一单跨承运商按量拆分未内置**：`Waybill.parent` 与 1:N 结构就绪，但缺少"把一张订单的货量/金额拆到多张子运单+各自承运商"的服务。
2. **按对手方对账无幂等/去重**：`generate_statement_for_batch` 幂等（靠 `batch.statement_no`），但 `generate_statement` 无"已对账"标记，账期重叠时同一 `ExpenseRecord` 可能被两张单重复归集。建议给 `ExpenseRecord` 增加 `statemented_at` / 反向 FK。
3. **异常扣款不流入对账数学**：`Statement.total_amount = Σ ExpenseRecord.amount`；异常/索赔金额未转成负向明细。建议异常闭环时落一条 `external`/负向 `ExpenseRecord`。
4. **无 `Statement → Waybill/Order/Batch` 直接外键**：可追溯性依赖 `StatementLine.expense_record → ExpenseRecord.waybill` 与 `StatementLine.waybill_no` 冗余串。`GET /orders/{id}/lineage` 提供正向血缘视图（见 §7）。

## 7. 可追溯性 API 与 UI

- **接口**：`GET /api/v1/orders/{id}/lineage` 返回该订单的完整单据血缘图：
  订单 → 各运单（含承运商、批次、应收/应付明细）→ 命中的应收/应付对账单（含结算状态）。
- **前端**：订单详情页「单据关系（血缘）」面板，一屏看清 DD → YD → ST 全链路与金额、结算进度，
  支撑上述所有复杂场景的下钻核对。
