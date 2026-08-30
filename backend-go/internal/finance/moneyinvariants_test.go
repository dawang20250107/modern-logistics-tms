package finance_test

// 钱的三条守恒，对**当前这个库**判一次。
//
// 与各功能的单元用例不同：那些验的是"这一步算得对不对"，
// 这一条验的是"整本账现在自不自洽"。前者全绿而后者不成立，是可能的——
// 演示数据就出过一次：settled_amount 写成六成，**一条核销明细都没有**，
// 界面写着「已核销 ¥37,800」，点进核销记录是空的。
// `settled_amount == Σ 核销明细` 正是核销逻辑（事务 + FOR UPDATE +
// 未结余额校验）一直在维持的不变式，而数据从第一天起就不满足它。
//
// 这三条是**系统级不变式**，任何一个库都该成立，所以放在 go test 里
// （CI 每次都会在新播种的库上跑一遍）；而"在途运单要有司机""已签收要有事件"
// 那类是演示数据的形状，放在 coldstart.sh 里对新库判。
//
// 需要真实 Postgres；无 DATABASE_URL 则跳过。

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMoneyInvariantsHoldOnThisDatabase(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("未设置 DATABASE_URL，跳过账目守恒检查")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连库失败：%v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("库 ping 不通：%v", err)
	}

	cases := []struct {
		name, sql, why string
	}{
		{
			"对账单表头金额 = 明细行合计",
			`SELECT count(*) FROM (
			   SELECT s.id FROM fin_statement s
			   LEFT JOIN fin_statement_line l ON l.statement_id = s.id
			   GROUP BY s.id, s.total_amount
			   HAVING s.total_amount <> COALESCE(sum(l.amount), 0)) t`,
			"表头是拿去跟客户对话的数字，明细是它的依据。两者对不上时，先信哪一个都没道理。",
		},
		{
			"已核销额 = 核销明细合计",
			`SELECT count(*) FROM (
			   SELECT s.id FROM fin_statement s
			   LEFT JOIN fin_statement_payment p ON p.statement_id = s.id
			   GROUP BY s.id, s.settled_amount
			   HAVING s.settled_amount <> COALESCE(sum(p.amount), 0)) t`,
			"标着已核销却没有核销记录，等于说钱收到了但拿不出是谁什么时候付的。",
		},
		{
			"核销额不超过应收额",
			`SELECT count(*) FROM fin_statement WHERE settled_amount > total_amount`,
			"超额核销就是账实不符：多认了钱进来，应收敞口凭空少一块。",
		},
		{
			"同一笔费用没有被两张对账单同时归集",
			`SELECT count(*) FROM (
			   SELECT expense_record_id FROM fin_statement_line
			   WHERE expense_record_id IS NOT NULL
			   GROUP BY expense_record_id HAVING count(*) > 1) t`,
			"一笔钱进两张单，就会被收两次。",
		},
	}
	for _, c := range cases {
		var n int
		if err := pool.QueryRow(ctx, c.sql).Scan(&n); err != nil {
			t.Fatalf("%s：查询失败 %v", c.name, err)
		}
		if n != 0 {
			t.Errorf("%s —— 有 %d 处不成立。\n  %s", c.name, n, c.why)
		}
	}
}
