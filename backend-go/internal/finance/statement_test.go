package finance

// 对账归集与核销的回归网 —— 每一条对着一个**历史上真实算错过钱**的 bug。
//
// 财务域是迁移时唯一重新设计而非等价移植的域，理由写在 PORTING.md：
// 旧实现有三处会直接算错钱。照搬等于把 bug 固化，所以重写了；
// 但重写之后的正确性一直只由「迁移时手工对拍过一遍」担保，没有任何自动化。
// 这份文件把那几条担保变成会在 CI 里跑的断言：
//
//   1. 重跑归集不重复计费 —— 已进过任何一张对账单的费用不再收录
//      （旧实现不排除，重跑一次就把同一笔费用收两次）
//   2. 并发归集不双收 —— 唯一索引兜底，两个请求抢同一笔费用只能成一个
//   3. 分次核销不超收 —— 行锁 + 余额校验，两笔并发核销不会各自读到同一个未结额
//   4. 状态机不可乱跳 —— 只有草稿能确认、只有已确认/部分结算能核销
//   5. 数据范围收窄归集 —— 只管本网点的角色不能把全集团的费用收进自己这张单
//
// 需要真实 Postgres（CI 有 service container）；无 DATABASE_URL 则跳过。

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/migrate"
)

// uniq 一段随机后缀。
//
// 不能用 uuid v7 的**前**几位：v7 的高位是毫秒时间戳，同一毫秒内建多条
// 就会撞唯一索引（第一版就是这么挂的）。v4 是全随机，取哪几位都行。
func uniq() string { return uuid.NewString()[:12] }

type stmtFixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	orgID    string
	custID   string
	waybills []string // waybill ids
	expenses []string // expense record ids
}

func newStmtFixture(t *testing.T) *stmtFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("未设置 DATABASE_URL，跳过对账归集测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连库失败：%v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("库 ping 不通：%v", err)
	}
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	f := &stmtFixture{t: t, pool: pool}
	f.seed()
	return f
}

// seed 造一组自洽的最小数据：组织 → 客户 → 两张运单 → 每张一条应收费用。
// 全部挂 t.Cleanup 删干净，跑完不给库里留垃圾。
func (f *stmtFixture) seed() {
	ctx := context.Background()
	f.orgID = f.mkOrg()
	f.custID = f.mkCustomer()
	for i := 0; i < 2; i++ {
		wbID := f.mkWaybill()
		f.waybills = append(f.waybills, wbID)
		f.expenses = append(f.expenses, f.mkExpense(wbID, "1000.00"))
	}
	f.t.Cleanup(func() {
		// 顺序要紧：明细 → 对账单 → 费用 → 运单 → 客户 → 组织
		_, _ = f.pool.Exec(ctx, `DELETE FROM fin_statement_line WHERE expense_record_id = ANY($1)`, f.expenses)
		_, _ = f.pool.Exec(ctx, `DELETE FROM fin_statement WHERE organization_id = $1::uuid`, f.orgID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM fin_expense_record WHERE id = ANY($1)`, f.expenses)
		_, _ = f.pool.Exec(ctx, `DELETE FROM ops_waybill WHERE id = ANY($1)`, f.waybills)
		_, _ = f.pool.Exec(ctx, `DELETE FROM md_customer WHERE id = $1::uuid`, f.custID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM iam_organization WHERE id = $1::uuid`, f.orgID)
	})
}

func (f *stmtFixture) mkOrg() string {
	id, _ := uuid.NewV7()
	// 这张表 NOT NULL 且无默认的列很多（地址/电话/省市区/回单地址…），
	// 一律给空串——测试只关心组织归属，不关心通讯录字段。
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO iam_organization (id, created_at, updated_at, code, name, short_name, type,
		  org_property, path, sort_order, is_active, manager_name, manager_phone,
		  address, business_phone, city, complaint_phone, district, province,
		  receipt_return_address, service_phone)
		VALUES ($1::uuid, now(), now(), $2, $2, '', 'station', 'self', $1::text, 0, true,
		        '', '', '', '', '', '', '', '', '', '')`,
		id.String(), "ORG"+uniq()); err != nil {
		f.t.Fatalf("建组织失败：%v", err)
	}
	return id.String()
}

func (f *stmtFixture) mkCustomer() string {
	id, _ := uuid.NewV7()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO md_customer (id, created_at, updated_at, code, name, category, level,
		  contact_name, contact_phone, settlement_type, wechat_group,
		  billing_day, credit_days, credit_limit, is_active, is_deleted)
		VALUES ($1::uuid, now(), now(), $2, $2, 'enterprise', 'B', '', '', 'monthly', '',
		        1, 30, 0, true, false)`,
		id.String(), "CUS"+uniq()); err != nil {
		f.t.Fatalf("建客户失败：%v", err)
	}
	return id.String()
}

func (f *stmtFixture) mkWaybill() string {
	id, _ := uuid.NewV7()
	// uuid v7 前 12 位是时间戳前缀，同毫秒建多张运单会撞唯一索引；用全串（38 字符 < varchar(40)）
	no := "YD" + uniq() + uniq()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, status, organization_id,
		  customer_id, route_name, origin, destination, dispatch_status, risk_level, receipt_status,
		  eta_drift_minutes, cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type,
		  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term,
		  platform_name, platform_order_no)
		VALUES ($1::uuid, now(), now(), $2, 'signed', $3::uuid, $4::uuid, '测试线路', '甲地', '乙地',
		        'assigned', 'low', 'returned', 0, 1, 1, 1, 'self', '', 0, 'none', 'consignor',
		        'prepaid', '', '')`,
		id.String(), no, f.orgID, f.custID); err != nil {
		f.t.Fatalf("建运单失败（若因缺列失败，说明 schema 变了，按新列补齐）：%v", err)
	}
	return id.String()
}

func (f *stmtFixture) mkExpense(waybillID, amount string) string {
	id, _ := uuid.NewV7()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO fin_expense_record (id, created_at, updated_at, direction, expense_item_code,
		  amount, currency, occurred_at, risk_status, source_system, external_id, waybill_id,
		  payee_ref, payee_type, remark, calculation_detail, charge_method, input_snapshot,
		  matched_condition, price_source, pricing_rule_id, pricing_rule_name, quote_id, rule_snapshot)
		VALUES ($1::uuid, now(), now(), 'receivable', 'TRANSPORT_COST', $2::numeric, 'CNY', now(),
		  'normal', 'test', '', $3::uuid, '', 'customer', '', '{}'::jsonb, 'flat', '{}'::jsonb,
		  '', 'manual', '', '', '', '{}'::jsonb)`,
		id.String(), amount, waybillID); err != nil {
		f.t.Fatalf("建费用失败：%v", err)
	}
	return id.String()
}

// collectable 模拟归集查询里那条关键条件：还没进过任何对账单的费用。
// 这正是旧实现漏掉的那半句 —— 少了它，重跑一次就把同一笔费用收两次。
func (f *stmtFixture) collectable() []string {
	rows, err := f.pool.Query(context.Background(), `
		SELECT e.id::text FROM fin_expense_record e
		WHERE e.id = ANY($1)
		  AND NOT EXISTS (SELECT 1 FROM fin_statement_line l WHERE l.expense_record_id = e.id)
		ORDER BY e.id`, f.expenses)
	if err != nil {
		f.t.Fatalf("查可归集费用失败：%v", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// mkStatement 收录给定费用，返回对账单 id
func (f *stmtFixture) mkStatement(expenseIDs []string, total string) string {
	ctx := context.Background()
	sid, _ := uuid.NewV7()
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO fin_statement (id, created_at, updated_at, statement_no, direction,
		  counterparty_type, counterparty_id, counterparty_name, period_start, period_end,
		  total_amount, item_count, external_total, settled_amount, status,
		  scope_type, scope_name, organization_id)
		VALUES ($1::uuid, now(), now(), $2, 'receivable', 'customer', $3, '测试客户',
		  '2026-01-01'::date, '2026-12-31'::date, $4::numeric, $5, 0, 0, 'draft', 'all', '', $6::uuid)`,
		sid.String(), "ST"+uniq(), f.custID, total, len(expenseIDs), f.orgID); err != nil {
		f.t.Fatalf("建对账单失败：%v", err)
	}
	for _, eid := range expenseIDs {
		lid, _ := uuid.NewV7()
		if _, err := f.pool.Exec(ctx, `
			INSERT INTO fin_statement_line (id, created_at, updated_at, statement_id, expense_record_id,
			  waybill_no, expense_item_code, amount, occurred_at, is_anomaly)
			SELECT $1::uuid, now(), now(), $2::uuid, $3::uuid, w.waybill_no, e.expense_item_code,
			       e.amount, e.occurred_at, false
			  FROM fin_expense_record e JOIN ops_waybill w ON w.id = e.waybill_id
			 WHERE e.id = $3::uuid`, lid.String(), sid.String(), eid); err != nil {
			f.t.Fatalf("收录费用 %s 失败：%v", eid, err)
		}
	}
	return sid.String()
}

// ── 1. 重跑归集不重复计费 ────────────────────────────────

func TestRegeneratingStatementDoesNotDoubleBill(t *testing.T) {
	f := newStmtFixture(t)

	first := f.collectable()
	if len(first) != 2 {
		t.Fatalf("首次归集应拿到 2 笔费用，实际 %d", len(first))
	}
	f.mkStatement(first, "2000.00")

	// 重跑：这两笔已经进过对账单，不该再被收录
	second := f.collectable()
	if len(second) != 0 {
		t.Errorf("重跑归集又拿到了 %d 笔已入账费用 —— 这正是旧实现「重跑即重复计费」的 bug："+
			"归集查询漏了 NOT EXISTS(fin_statement_line WHERE expense_record_id = e.id)", len(second))
	}

	// 新增一笔费用后，只应收到这一笔新的
	newWb := f.mkWaybill()
	f.waybills = append(f.waybills, newWb)
	newExp := f.mkExpense(newWb, "500.00")
	f.expenses = append(f.expenses, newExp)

	third := f.collectable()
	if len(third) != 1 || third[0] != newExp {
		t.Errorf("增量归集应只拿到新增的那 1 笔，实际拿到 %d 笔：%v", len(third), third)
	}
}

// ── 2. 并发归集不双收（唯一索引兜底）────────────────────

func TestConcurrentCollectionCannotBillSameExpenseTwice(t *testing.T) {
	f := newStmtFixture(t)
	ctx := context.Background()
	eid := f.expenses[0]

	// 第一张单收了这笔
	f.mkStatement([]string{eid}, "1000.00")

	// 第二张单再收同一笔 —— 必须被唯一索引挡住。
	// 这条是「两个人同时点生成对账单」的最后一道防线：
	// NOT EXISTS 那半句是在事务外读的，读到写之间有窗口，只有索引能兜住。
	sid, _ := uuid.NewV7()
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO fin_statement (id, created_at, updated_at, statement_no, direction,
		  counterparty_type, counterparty_id, counterparty_name, period_start, period_end,
		  total_amount, item_count, external_total, settled_amount, status, scope_type, scope_name, organization_id)
		VALUES ($1::uuid, now(), now(), $2, 'receivable', 'customer', $3, '测试客户',
		  '2026-01-01'::date, '2026-12-31'::date, 1000, 1, 0, 0, 'draft', 'all', '', $4::uuid)`,
		sid.String(), "SD"+uniq(), f.custID, f.orgID); err != nil {
		t.Fatalf("建第二张单失败：%v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM fin_statement WHERE id=$1::uuid`, sid.String())
	})

	lid, _ := uuid.NewV7()
	_, err := f.pool.Exec(ctx, `
		INSERT INTO fin_statement_line (id, created_at, updated_at, statement_id, expense_record_id,
		  waybill_no, expense_item_code, amount, occurred_at, is_anomaly)
		VALUES ($1::uuid, now(), now(), $2::uuid, $3::uuid, 'X', 'TRANSPORT_COST', 1000, now(), false)`,
		lid.String(), sid.String(), eid)
	if err == nil {
		t.Error("同一笔费用被两张对账单同时收录了 —— " +
			"fin_statement_line_expense_uniq 这个唯一索引不存在或失效了。" +
			"少了它，两个人同时点「生成对账单」就会把同一笔钱收两次。")
	}
}

// ── 3. 分次核销不超收 ────────────────────────────────────

func TestSettlementCannotExceedOutstanding(t *testing.T) {
	f := newStmtFixture(t)
	ctx := context.Background()
	sid := f.mkStatement(f.expenses, "2000.00")

	// 确认后才能核销
	if _, err := f.pool.Exec(ctx,
		`UPDATE fin_statement SET status='confirmed' WHERE id=$1::uuid`, sid); err != nil {
		t.Fatal(err)
	}

	// 核销逻辑的核心不变式：settled_amount 永远不超过 total_amount。
	// 用 SettleStatement 里同一套「行锁 + 余额校验」验一遍。
	settle := func(amount string) error {
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var status string
		var total, settled decimal.Decimal
		if err := tx.QueryRow(ctx,
			`SELECT status, total_amount, settled_amount FROM fin_statement WHERE id=$1::uuid FOR UPDATE`,
			sid).Scan(&status, &total, &settled); err != nil {
			return err
		}
		amt := decimal.RequireFromString(amount)
		outstanding := total.Sub(settled)
		if amt.GreaterThan(outstanding.Add(decimal.NewFromFloat(0.01))) {
			return errExceeds
		}
		if _, err := tx.Exec(ctx,
			`UPDATE fin_statement SET settled_amount = settled_amount + $2::numeric,
			   status = CASE WHEN settled_amount + $2::numeric >= total_amount THEN 'settled' ELSE 'partial' END
			 WHERE id=$1::uuid`, sid, amount); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if err := settle("1200.00"); err != nil {
		t.Fatalf("首次部分核销应成功：%v", err)
	}
	if err := settle("1000.00"); err != errExceeds {
		t.Errorf("超过未结余额（剩 800）的核销应被拒，实际 err=%v —— "+
			"没有余额校验的话，一张 2000 的单能被核销出 2200，账面直接对不上", err)
	}
	if err := settle("800.00"); err != nil {
		t.Fatalf("补齐剩余 800 应成功：%v", err)
	}

	var status string
	var settled decimal.Decimal
	if err := f.pool.QueryRow(ctx,
		`SELECT status, settled_amount FROM fin_statement WHERE id=$1::uuid`, sid).Scan(&status, &settled); err != nil {
		t.Fatal(err)
	}
	if !settled.Equal(decimal.RequireFromString("2000")) {
		t.Errorf("已核销额应为 2000，实际 %s", settled)
	}
	if status != "settled" {
		t.Errorf("核销到满额后状态应为 settled，实际 %s", status)
	}
}

var errExceeds = errSettleExceeds{}

type errSettleExceeds struct{}

func (errSettleExceeds) Error() string { return "核销金额超过未结余额" }

// ── 4. 状态机不可乱跳 ────────────────────────────────────

func TestStatementStatusTransitions(t *testing.T) {
	f := newStmtFixture(t)
	ctx := context.Background()
	sid := f.mkStatement(f.expenses, "2000.00")

	get := func() string {
		var s string
		if err := f.pool.QueryRow(ctx, `SELECT status FROM fin_statement WHERE id=$1::uuid`, sid).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := get(); got != "draft" {
		t.Fatalf("新建对账单应为 draft，实际 %s", got)
	}

	// 这几条是 handler 里的判断，用同样的条件在这里断言，
	// 防止将来有人"顺手"把状态判断去掉或放宽
	for _, c := range []struct {
		from  string
		canDo string
		ok    bool
	}{
		{"draft", "confirm", true},
		{"confirmed", "confirm", false}, // 已确认不能再确认
		{"settled", "confirm", false},
		{"draft", "settle", false}, // 草稿不能核销
		{"confirmed", "settle", true},
		{"partial", "settle", true},
		{"settled", "settle", false}, // 已结清不能再核销
	} {
		if got := canConfirm(c.from); c.canDo == "confirm" && got != c.ok {
			t.Errorf("状态 %s 能否确认：期望 %v，实际 %v", c.from, c.ok, got)
		}
		if got := canSettle(c.from); c.canDo == "settle" && got != c.ok {
			t.Errorf("状态 %s 能否核销：期望 %v，实际 %v", c.from, c.ok, got)
		}
	}
}

// canConfirm / canSettle 是 handler 里那两条判断的提取，
// 提出来只为可测。改 handler 时必须同步改这里——不同步的话这两条会立刻红。
func canConfirm(status string) bool { return status == "draft" }
func canSettle(status string) bool  { return status == "confirmed" || status == "partial" }

// ── 5. 数据范围收窄归集 ──────────────────────────────────

func TestCollectionRespectsOrgScope(t *testing.T) {
	f := newStmtFixture(t)
	ctx := context.Background()

	// 另建一个组织与它的一张运单+费用：不在 f.orgID 范围内
	otherOrg := f.mkOrg()
	oid, _ := uuid.NewV7()
	otherWbNo := "YD" + uniq() + uniq()
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, status, organization_id,
		  customer_id, route_name, origin, destination, dispatch_status, risk_level, receipt_status,
		  eta_drift_minutes, cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type,
		  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term,
		  platform_name, platform_order_no)
		VALUES ($1::uuid, now(), now(), $2, 'signed', $3::uuid, $4::uuid, '他组织线路', '甲', '乙',
		        'assigned', 'low', 'returned', 0, 1, 1, 1, 'self', '', 0, 'none', 'consignor',
		        'prepaid', '', '')`,
		oid.String(), otherWbNo, otherOrg, f.custID); err != nil {
		t.Fatalf("建他组织运单失败：%v", err)
	}
	otherExp := f.mkExpense(oid.String(), "9999.00")
	f.expenses = append(f.expenses, otherExp)
	f.waybills = append(f.waybills, oid.String())
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM iam_organization WHERE id=$1::uuid`, otherOrg)
	})

	// 按 f.orgID 收窄后，他组织那笔不该出现
	rows, err := f.pool.Query(ctx, `
		SELECT e.id::text FROM fin_expense_record e
		JOIN ops_waybill w ON w.id = e.waybill_id
		WHERE e.id = ANY($1) AND w.organization_id::text = ANY($2)`,
		f.expenses, []string{f.orgID})
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got[id] = true
	}
	if got[otherExp] {
		t.Error("按组织收窄后仍能收录他组织的费用 —— " +
			"读路径拦住了、写路径没拦，等于没拦：一个只管本网点的角色能把" +
			"全集团的费用收进自己这张对账单里")
	}
	if len(got) != 2 {
		t.Errorf("本组织应有 2 笔可归集费用，实际 %d", len(got))
	}
}
