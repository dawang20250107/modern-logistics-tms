// 演示数据播种：把一个空库填成「能演示、能测、能一眼看出哪里坏了」的样子。
//
//	go run ./cmd/seed          # 幂等，可重复执行
//	go run ./cmd/seed -reset   # 先清掉本命令造的数据再重播
//
// 为什么要有它：Django 退役时 seed_demo / seed_org / seed_realistic 三个
// 管理命令一并删掉了，于是「从零建库」之后是一片空白——
// 新部署没法演示，CI 里的数据范围用例因为库里没订单只能 skip，
// 前端走查也看不出「这一屏是设计得空，还是数据没造」。
//
// 数据是**一条完整的业务链**，不是一堆孤立的行：
// 组织树 → 角色与账号 → 客户/承运商/车辆/司机 → 计价规则 →
// 订单 → 运单 → 费用 → 对账单。链路通了，每个页面才都有东西看。
//
// 所有写入按业务唯一键 upsert（组织按 code、运单按 waybill_no……），
// 重复执行不会翻倍。带 SEED_ 前缀便于 -reset 精确清理，
// 不会误伤真实数据。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
)

// 统一前缀：-reset 靠它精确清理，也让人一眼看出哪些是演示数据
const pfx = "SEED_"

type seeder struct {
	ctx  context.Context
	db   *pgxpool.Pool
	errs int
}

func (s *seeder) exec(what, sql string, args ...any) {
	if _, err := s.db.Exec(s.ctx, sql, args...); err != nil {
		slog.Error("播种失败", "what", what, "err", err)
		s.errs++
	}
}

// id 由业务键派生出稳定 UUID，这样重复执行拿到的是同一行，
// 外键也不会因为重跑而指向新 id。
func id(key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(pfx+key)).String()
}

func main() {
	reset := flag.Bool("reset", false, "先清掉本命令造的演示数据再重播")
	flag.Parse()

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("连库失败", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		slog.Error("库 ping 不通", "err", err)
		os.Exit(1)
	}

	s := &seeder{ctx: ctx, db: db}
	if *reset {
		s.reset()
	}
	// 权限点目录：角色要授权限，得先有权限点可授
	if err := auth.EnsurePermissions(ctx, db); err != nil {
		slog.Error("权限目录失败", "err", err)
		os.Exit(1)
	}

	s.orgs()
	s.rolesAndUsers()
	s.customers()
	s.carriers()
	s.vehiclesAndDrivers()
	s.pricingRules()
	s.ordersAndWaybills()
	s.expensesAndStatements()

	if s.errs > 0 {
		slog.Error("播种完成但有失败项", "失败数", s.errs)
		os.Exit(1)
	}
	slog.Info("演示数据已就绪",
		"组织", 4, "账号", 4, "客户", 5, "承运商", 4, "车辆", 6, "司机", 5,
		"计价规则", 3, "订单", 12, "运单", 8)
	fmt.Println(`
账号（口令统一 Demo12345!）：
  seed_admin      超级管理员，看全部
  seed_dispatcher 调度员，本组织及下级
  seed_finance    财务，只有 finance.view（用来验"能看不能核销"）
  seed_cs         客服，只看本网点

提示：这些账号是演示用的弱口令，正式环境跑完 seed 请删掉或改密。`)
}

// reset 只删本命令造的行，靠 SEED_ 前缀与派生 UUID 精确定位。
// 顺序是外键的逆序——先删引用方，再删被引用方。
func (s *seeder) reset() {
	for _, q := range []string{
		`DELETE FROM fin_statement_line WHERE statement_id IN (SELECT id FROM fin_statement WHERE statement_no LIKE 'SEED%')`,
		`DELETE FROM fin_statement WHERE statement_no LIKE 'SEED%'`,
		`DELETE FROM fin_expense_record WHERE source_system = 'seed'`,
		`DELETE FROM ops_waybill WHERE waybill_no LIKE 'SEEDYD%'`,
		`DELETE FROM ops_order WHERE order_no LIKE 'SEEDDD%'`,
		`DELETE FROM fin_pricing_rule WHERE name LIKE 'SEED_%'`,
		`DELETE FROM md_vehicle WHERE plate_no LIKE '演%'`,
		`DELETE FROM md_driver WHERE name LIKE 'SEED_%'`,
		`DELETE FROM md_carrier WHERE code LIKE 'SEED_%'`,
		`DELETE FROM md_customer WHERE code LIKE 'SEED_%'`,
		`DELETE FROM iam_role_assignment WHERE user_id IN (SELECT id FROM accounts_user WHERE username LIKE 'seed_%')`,
		`DELETE FROM iam_role_permissions WHERE role_id IN (SELECT id FROM iam_role WHERE code LIKE 'SEED_%')`,
		// 登录审计与令牌黑名单都外键指向用户。演示账号只要登录过一次
		// （走查、演示、跑一遍权限验证都会），这两张表就有行，
		// 于是 accounts_user 删不掉，接着 iam_organization 也删不掉——
		// -reset 从第二次起就一直报两条错。删之前先把引用清掉。
		`DELETE FROM iam_login_attempt WHERE user_id IN (SELECT id FROM accounts_user WHERE username LIKE 'seed_%')`,
		`DELETE FROM iam_token_denylist WHERE user_id IN (SELECT id FROM accounts_user WHERE username LIKE 'seed_%')`,
		`DELETE FROM ntf_notification WHERE recipient_id IN (SELECT id FROM accounts_user WHERE username LIKE 'seed_%')`,
		`DELETE FROM accounts_user WHERE username LIKE 'seed_%'`,
		`DELETE FROM iam_role WHERE code LIKE 'SEED_%'`,
		`DELETE FROM iam_organization WHERE code LIKE 'SEED_%'`,
	} {
		s.exec("reset", q)
	}
	slog.Info("已清理旧演示数据")
}

// ── 组织树：集团 → 华东公司 → 上海网点 / 杭州网点 ──────────
//
// path 是物化路径，数据范围的 org_sub 档靠它做前缀匹配子树，
// 拼错的话「本组织及下级」会退化成「只有本组织」。

func (s *seeder) orgs() {
	type org struct{ code, name, typ, parent, path string }
	group := id("org/GROUP")
	east := id("org/EAST")
	for _, o := range []org{
		{"SEED_GROUP", "示例物流集团", "group", "", group},
		{"SEED_EAST", "华东公司", "company", group, group + "/" + east},
		{"SEED_SH", "上海网点", "station", east, group + "/" + east + "/" + id("org/SH")},
		{"SEED_HZ", "杭州网点", "station", east, group + "/" + east + "/" + id("org/HZ")},
	} {
		key := "org/" + o.code[len(pfx):]
		var parent any
		if o.parent != "" {
			parent = o.parent
		}
		s.exec("org "+o.code, `
			INSERT INTO iam_organization (id, created_at, updated_at, code, name, short_name, type,
			  org_property, path, sort_order, is_active, parent_id, manager_name, manager_phone,
			  address, business_phone, city, complaint_phone, district, province,
			  receipt_return_address, service_phone)
			VALUES ($1::uuid, now(), now(), $2, $3, $3, $4, 'self', $5, 0, true, $6::uuid,
			        '', '', '', '', '', '', '', '', '', '')
			ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, path=EXCLUDED.path,
			  parent_id=EXCLUDED.parent_id, updated_at=now()`,
			id(key), o.code, o.name, o.typ, o.path, parent)
	}
}

// ── 角色与账号 ───────────────────────────────────────────
//
// 四个账号刻意覆盖四种「看得见多少」的组合，这样任何一次权限改动
// 都能用它们当场验：超管看全部、调度看子树、财务只读、客服只看本网点。

func (s *seeder) rolesAndUsers() {
	type role struct {
		code, name, scope string
		perms             []string
	}
	roles := []role{
		{"SEED_DISPATCH", "调度员", "org_sub",
			[]string{"waybill.view", "waybill.manage", "masterdata.view", "carrier.view", "telematics.view"}},
		{"SEED_FINANCE", "财务（只读）", "all",
			[]string{"finance.view", "waybill.view", "analytics.view"}},
		{"SEED_CS", "客服", "org",
			[]string{"waybill.view", "masterdata.view"}},
	}
	for _, r := range roles {
		rid := id("role/" + r.code)
		s.exec("role "+r.code, `
			INSERT INTO iam_role (id, created_at, updated_at, code, name, data_scope, is_active)
			VALUES ($1::uuid, now(), now(), $2, $3, $4, true)
			ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, data_scope=EXCLUDED.data_scope, updated_at=now()`,
			rid, r.code, r.name, r.scope)
		for _, p := range r.perms {
			s.exec("role perm", `
				INSERT INTO iam_role_permissions (role_id, permission_id)
				SELECT $1::uuid, id FROM iam_permission WHERE code=$2
				ON CONFLICT DO NOTHING`, rid, p)
		}
	}

	hash := auth.MakeDjangoPassword("Demo12345!")
	type user struct {
		name, nick, org, role string
		super                 bool
	}
	for _, u := range []user{
		{"seed_admin", "演示超管", "", "", true},
		{"seed_dispatcher", "演示调度", "org/EAST", "SEED_DISPATCH", false},
		{"seed_finance", "演示财务", "org/GROUP", "SEED_FINANCE", false},
		{"seed_cs", "演示客服", "org/SH", "SEED_CS", false},
	} {
		uid := id("user/" + u.name)
		var orgID any
		if u.org != "" {
			orgID = id(u.org)
		}
		s.exec("user "+u.name, `
			INSERT INTO accounts_user (id, password, last_login, is_superuser, username, first_name,
			  last_name, email, is_staff, is_active, date_joined, phone, nickname, organization_id,
			  avatar, preferences)
			VALUES ($1::uuid, $2, NULL, $3, $4, '', '', '', $3, true, now(), '', $5, $6::uuid, NULL, '{}'::jsonb)
			ON CONFLICT (username) DO UPDATE SET password=EXCLUDED.password,
			  is_superuser=EXCLUDED.is_superuser, organization_id=EXCLUDED.organization_id,
			  nickname=EXCLUDED.nickname`,
			uid, hash, u.super, u.name, u.nick, orgID)
		if u.role != "" {
			s.exec("assign "+u.name, `
				INSERT INTO iam_role_assignment (id, created_at, updated_at, user_id, role_id)
				SELECT $1::uuid, now(), now(), $2::uuid, $3::uuid
				WHERE NOT EXISTS (SELECT 1 FROM iam_role_assignment WHERE user_id=$2::uuid AND role_id=$3::uuid)`,
				id("assign/"+u.name), uid, id("role/"+u.role))
		}
	}
}

func (s *seeder) customers() {
	type c struct{ code, name, level string }
	// 名字刻意与既有演示数据不重名。第一版用了「美的集团」「海尔智家」这些
	// 库里已经有的名字，结果命令面板搜「美的」出来两个同名客户、
	// 驾驶舱的「应收 Top 对手方」也并排列两行「美的集团」——
	// 看着像去重坏了，其实是两条真实存在的不同客户记录。
	// 演示数据要能一眼认出是演示数据，也不能给真数据制造歧义。
	for _, x := range []c{
		{"SEED_C1", "演示·华东制造", "S"}, {"SEED_C2", "演示·家电集团", "A"},
		{"SEED_C3", "演示·新能源物流", "A"}, {"SEED_C4", "演示·汽车供应链", "B"},
		{"SEED_C5", "演示·小微客户", "C"},
	} {
		s.exec("customer "+x.code, `
			INSERT INTO md_customer (id, created_at, updated_at, code, name, category, level,
			  contact_name, contact_phone, settlement_type, wechat_group,
			  billing_day, credit_days, credit_limit, is_active, is_deleted)
			VALUES ($1::uuid, now(), now(), $2, $3, 'enterprise', $4, '联系人', '13800000000',
			        'monthly', '', 1, 30, 500000, true, false)
			ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, level=EXCLUDED.level, updated_at=now()`,
			id("cust/"+x.code), x.code, x.name, x.level)
	}
}

func (s *seeder) carriers() {
	type c struct {
		code, name, city, grade, typ string
	}
	for _, x := range []c{
		{"SEED_R1", "演示·甲承运", "上海", "A", "fleet"},
		{"SEED_R2", "演示·乙物流", "杭州", "B", "fleet"},
		{"SEED_R3", "演示·丙运输", "上海", "B", "individual"},
		{"SEED_R4", "演示·丁快运", "宁波", "C", "individual"},
	} {
		s.exec("carrier "+x.code, `
			INSERT INTO md_carrier (id, created_at, updated_at, code, name, contact_name, contact_phone,
			  settlement_type, is_active, is_deleted, billing_day, blacklist_reason, blacklisted,
			  business_license_no, credit_days, credit_limit, grade, carrier_type, city,
			  service_area, tax_no, transport_license_no)
			VALUES ($1::uuid, now(), now(), $2, $3, '联系人', '13900000000', 'monthly', true, false,
			        1, '', false, '', 30, 300000, $4, $5, $6, '华东', '', '')
			ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, grade=EXCLUDED.grade, updated_at=now()`,
			id("carrier/"+x.code), x.code, x.name, x.grade, x.typ, x.city)
	}
}

func (s *seeder) vehiclesAndDrivers() {
	for i := 1; i <= 6; i++ {
		plate := fmt.Sprintf("演A%05d", i)
		s.exec("vehicle "+plate, `
			INSERT INTO md_vehicle (id, created_at, updated_at, plate_no, vehicle_type, ownership_type,
			  is_active, is_deleted, road_transport_cert_no, load_capacity_ton, volume_capacity_cbm,
			  vehicle_class, dispatch_source, body_type, vehicle_length_m)
			VALUES ($1::uuid, now(), now(), $2, 'truck', 'owned', true, false, '', $3, $4,
			        'medium', 'self', 'van', 9.6)
			ON CONFLICT (plate_no) DO UPDATE SET updated_at=now()`,
			id(fmt.Sprintf("vehicle/%d", i)), plate, 10+i, 40+i*2)
	}
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("SEED_司机%d", i)
		s.exec("driver "+name, `
			INSERT INTO md_driver (id, created_at, updated_at, name, phone, id_no, license_no,
			  is_active, is_deleted, license_type, qualification_cert_no, employment_type,
			  app_registered, cumulative_freight, cumulative_waybills, wechat)
			VALUES ($1::uuid, now(), now(), $2, $3, '', '', true, false, 'A2', '', 'employee',
			        false, 0, 0, '')
			ON CONFLICT DO NOTHING`,
			id(fmt.Sprintf("driver/%d", i)), name, fmt.Sprintf("1370000%04d", i))
	}
}

// pricingRules 三条规则刚好覆盖三种最常用的计费方式，
// 也让「计价规则」那一页不是空的。
func (s *seeder) pricingRules() {
	type r struct {
		name, priceType, method, base, unit, minPrice, fuel, tiers string
	}
	for _, x := range []r{
		{"SEED_华东整车收入价", "income", "flat", "3500", "0", "3000", "0.025", "[]"},
		{"SEED_零担按重阶梯", "income", "tiered_weight", "0", "0", "200", "0",
			`[{"min_ton":"0","max_ton":"5","price":"260"},{"min_ton":"5","max_ton":"15","price":"220"},{"min_ton":"15","max_ton":"30","price":"190"}]`},
		{"SEED_承运成本价", "cost", "per_km", "300", "6.8", "500", "0", "[]"},
	} {
		s.exec("rule "+x.name, `
			INSERT INTO fin_pricing_rule (id, created_at, updated_at, name, price_type, expense_item_code,
			  route_name, vehicle_type, base_price, min_price, priority, is_active, fuel_surcharge_pct,
			  tier_prices, volumetric_factor, charge_method, min_charge_qty, unit_price)
			VALUES ($1::uuid, now(), now(), $2, $3, 'TRANSPORT_COST', '', '', $4::numeric, $5::numeric,
			        10, true, $6::numeric, $7::jsonb, 0.333, $8, 0, $9::numeric)
			ON CONFLICT DO NOTHING`,
			id("rule/"+x.name), x.name, x.priceType, x.base, x.minPrice, x.fuel, x.tiers, x.method, x.unit)
	}
}

// ordersAndWaybills 12 张订单铺开在各个状态上，其中 8 张转成运单。
// 状态分布是刻意的：驾驶舱的漏斗、调度台的三个池、订单管理的状态列
// 都需要"每一档都有货"才看得出对不对。
func (s *seeder) ordersAndWaybills() {
	custs := []string{"SEED_C1", "SEED_C2", "SEED_C3", "SEED_C4", "SEED_C5"}
	orgs := []string{"org/SH", "org/HZ"}
	statuses := []string{
		"draft", "pending_confirm", "pending_confirm", "confirmed",
		"pooled", "pooled", "dispatching", "converted",
		"converted", "converted", "completed", "completed",
	}
	routes := [][2]string{{"上海", "成都"}, {"杭州", "重庆"}, {"苏州", "西安"}, {"宁波", "长沙"}}

	// pooled_at 在 Go 里算好再传：原先写成 CASE WHEN $4 = 'pooled'，
	// 同一个占位符既当 status 列的 varchar 又参与文本比较，
	// PG 推不出一致类型直接报 42P08。参数只承担一种角色，SQL 才不用猜。
	pooledAt := func(status string) any {
		if status == "pooled" {
			return time.Now()
		}
		return nil
	}

	for i, st := range statuses {
		no := fmt.Sprintf("SEEDDD%06d", i+1)
		route := routes[i%len(routes)]
		s.exec("order "+no, `
			INSERT INTO ops_order (id, created_at, updated_at, order_no, source, status, remark,
			  cargo_desc, cargo_quantity, cargo_volume_cbm, cargo_weight_ton, channel,
			  contact_name, contact_phone, destination, origin, parse_meta, raw_text,
			  business_type, cargo_value, delivery_address, delivery_contact_name, delivery_contact_phone,
			  is_deleted, is_hazardous, package_type, pickup_address, pickup_contact_name,
			  pickup_contact_phone, priority, quoted_amount, settlement_type, source_type,
			  temperature_range, sla_status, approval_remark, approval_status, ai_conversation_id,
			  cod_amount, cod_status, freight_payer, freight_term,
			  customer_id, created_by_id, pooled_at)
			VALUES ($1::uuid, now() - ($2 || ' days')::interval, now(), $3, 'cs', $4, '',
			        '演示货物', $5, $6::numeric, $7::numeric, 'cs', '联系人', '13800000000',
			        $8, $9, '{}'::jsonb, '', 'ftl', 0, '', '', '', false, false, $14,
			        '', '', '', 'normal', $10::numeric, 'monthly', $15, '', 'pending',
			        '', 'none', '', 0, 'none', 'consignor', 'prepaid',
			        $11::uuid, $12::uuid, $13::timestamptz)
			ON CONFLICT (order_no) DO UPDATE SET status=EXCLUDED.status, updated_at=now()`,
			id("order/"+no), fmt.Sprint(i%14), no, st,
			10+i*3, 2.5+float64(i), 8.0+float64(i), route[1], route[0],
			12000+i*1500,
			id("cust/"+custs[i%len(custs)]), id("user/seed_"+[]string{"cs", "dispatcher"}[i%2]),
			pooledAt(st),
			// package_type 是自由文本（建单表单的占位符就写着「例: 托盘 / 木箱 / 裸装」），
			// source_type 的取值是 enterprise/individual —— 这两个字段第一版分别写成了
			// 'pallet' 和 'manual'，界面上就显示成「包装 pallet」「客户分类 manual」。
			// 那看着像是界面漏了枚举翻译，其实是**播种数据用错了词汇表**。
			// 演示数据必须长得像真数据，否则拿它走查界面会走出一堆假 bug。
			[]string{"托盘", "木箱", "裸装", "纸箱"}[i%4],
			[]string{"enterprise", "individual"}[i%2])
	}

	// 后 8 张转运单，状态铺满在途 → 已签收
	wbStatuses := []string{"dispatched", "loaded", "departed", "in_transit", "in_transit", "arrived", "signed", "signed"}
	for i, st := range wbStatuses {
		orderIdx := i + 4 // 从第 5 张订单开始转
		no := fmt.Sprintf("SEEDYD%06d", i+1)
		route := routes[orderIdx%len(routes)]
		s.exec("waybill "+no, `
			INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, route_name, origin,
			  destination, status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
			  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id,
			  cod_amount, cod_status, freight_payer, freight_term, platform_name, platform_order_no,
			  customer_id, carrier_id, organization_id, order_id, vehicle_id, driver_id)
			VALUES ($1::uuid, now() - ($2 || ' days')::interval, now(), $3, $4, $5, $6, $7,
			        'assigned', 'low', $8, 0, 20, 12.5, 30, 'outsource', '', 0, 'none',
			        'consignor', 'prepaid', '', '',
			        $9::uuid, $10::uuid, $11::uuid, $12::uuid, $13::uuid, $14::uuid)
			ON CONFLICT (waybill_no) DO UPDATE SET status=EXCLUDED.status, updated_at=now()`,
			id("waybill/"+no), fmt.Sprint(i+2), no, route[0]+"→"+route[1], route[0], route[1], st,
			map[bool]string{true: "returned", false: "pending"}[st == "signed"],
			id("cust/"+custs[orderIdx%len(custs)]),
			id("carrier/SEED_R"+fmt.Sprint(i%4+1)),
			id(orgs[orderIdx%len(orgs)]),
			id(fmt.Sprintf("order/SEEDDD%06d", orderIdx+1)),
			id(fmt.Sprintf("vehicle/%d", i%6+1)),
			id(fmt.Sprintf("driver/%d", i%5+1)))
	}
}

// expensesAndStatements 每张运单一笔应收 + 一笔应付，前 4 张进一张对账单。
// 留一部分不进对账单是有意的：对账中心要能看出「还有多少没归集」。
func (s *seeder) expensesAndStatements() {
	for i := 1; i <= 8; i++ {
		wbNo := fmt.Sprintf("SEEDYD%06d", i)
		for _, d := range []struct {
			dir, amount, payee string
		}{
			{"receivable", fmt.Sprint(12000 + i*1500), "customer"},
			{"payable", fmt.Sprint(9000 + i*1100), "carrier"},
		} {
			s.exec("expense "+wbNo+d.dir, `
				INSERT INTO fin_expense_record (id, created_at, updated_at, direction, expense_item_code,
				  amount, currency, occurred_at, risk_status, source_system, external_id, waybill_id,
				  payee_ref, payee_type, remark, calculation_detail, charge_method, input_snapshot,
				  matched_condition, price_source, pricing_rule_id, pricing_rule_name, quote_id, rule_snapshot)
				SELECT $1::uuid, now(), now(), $2, 'TRANSPORT_COST', $3::numeric, 'CNY',
				       w.created_at, 'normal', 'seed', '', w.id, '', $4, '演示费用', '{}'::jsonb,
				       'flat', '{}'::jsonb, '', 'manual', '', '', '', '{}'::jsonb
				  FROM ops_waybill w WHERE w.waybill_no = $5
				ON CONFLICT DO NOTHING`,
				id("expense/"+wbNo+"/"+d.dir), d.dir, d.amount, d.payee, wbNo)
		}
	}

	// 一张已确认、部分结算的对账单：待核销队列要有货，账龄分析也才有分桶
	sid := id("statement/SEED1")
	s.exec("statement", `
		INSERT INTO fin_statement (id, created_at, updated_at, statement_no, direction,
		  counterparty_type, counterparty_id, counterparty_name, period_start, period_end, due_date,
		  total_amount, item_count, external_total, settled_amount, status, confirmed_at,
		  scope_type, scope_name, organization_id)
		SELECT $1::uuid, now() - interval '20 days', now(), 'SEEDST000001', 'receivable',
		       'customer', $2::text, c.name, (now() - interval '60 days')::date, (now() - interval '1 day')::date,
		       (now() - interval '5 days')::date,
		       0, 0, 0, 0, 'confirmed', now() - interval '10 days', 'all', '', $4::uuid
		  FROM md_customer c WHERE c.id = $3::uuid
		ON CONFLICT DO NOTHING`,
		sid, id("cust/SEED_C1"), id("cust/SEED_C1"), id("org/SH"))

	// 收录前 4 张运单的应收，然后把合计与部分结算额回写
	for i := 1; i <= 4; i++ {
		wbNo := fmt.Sprintf("SEEDYD%06d", i)
		s.exec("statement line "+wbNo, `
			INSERT INTO fin_statement_line (id, created_at, updated_at, statement_id, expense_record_id,
			  waybill_no, expense_item_code, amount, occurred_at, is_anomaly)
			SELECT $1::uuid, now(), now(), $2::uuid, e.id, $3::text, e.expense_item_code, e.amount,
			       e.occurred_at, false
			  FROM fin_expense_record e
			  JOIN ops_waybill w ON w.id = e.waybill_id
			 WHERE w.waybill_no = $3::text AND e.direction = 'receivable'
			   AND NOT EXISTS (SELECT 1 FROM fin_statement_line l WHERE l.expense_record_id = e.id)
			ON CONFLICT DO NOTHING`,
			id("line/"+wbNo), sid, wbNo)
	}
	s.exec("statement totals", `
		UPDATE fin_statement s SET
		  total_amount = COALESCE(t.sum_amt, 0),
		  item_count   = COALESCE(t.n, 0),
		  -- 部分结算：收了六成，让"待核销队列"和"账龄"都有真实的未结额
		  settled_amount = round(COALESCE(t.sum_amt, 0) * 0.6, 2),
		  status = CASE WHEN COALESCE(t.n,0) = 0 THEN 'draft' ELSE 'partial' END,
		  updated_at = now()
		  FROM (SELECT statement_id, sum(amount) AS sum_amt, count(*) AS n
		          FROM fin_statement_line GROUP BY statement_id) t
		 WHERE t.statement_id = s.id AND s.id = $1::uuid`, sid)
}
