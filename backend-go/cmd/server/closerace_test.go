package main

// 状态守卫读在事务外 —— 并发下等于没有守卫。
//
// 这两处的写法都是：先 `h.DB.QueryRow` 读一次状态做前置校验，
// 通过了再 `h.DB.Exec` 写。两条语句之间没有锁、没有事务，
// UPDATE 也不带状态条件。串行点两次会被第一条挡住，看起来是好的；
// 两个人同时点（或者前端重试、双击）就一起穿过去。
//
// 挡不住的两件事都直接生成钱：
//   关闭异常  → 责任金额 > 0 落一条应付，两次并发就是两条，赔付翻倍
//   审批/驳回 → 审批已经落了应付和付款申请，驳回还能把它改成"已驳回"，
//               于是一笔被否掉的报销挂着待付的钱

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestConcurrentExceptionCloseChargesOnce 同时关闭同一条异常，只能计一次赔付。
func TestConcurrentExceptionCloseChargesOnce(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	excID := e.mkException(token, wbID, "high")

	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = e.call(token, "POST", "/api/v1/exceptions/"+excID+"/close",
				`{"responsibility_party":"carrier","amount":"800","resolution":"并发用例"}`).Code
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, c := range codes {
		if c < 300 {
			ok++
		}
	}
	t.Logf("并发关闭的返回码：%v", codes)
	for i, c := range codes {
		if c >= 500 {
			t.Errorf("第 %d 个并发关闭拿到 %d —— 并发冲突不该表现成服务端故障", i, c)
		}
	}

	cnt, sum := e.exceptionCosts(excID)
	if cnt != 1 {
		t.Errorf("%d 个并发关闭落了 %d 条应付（合计 %s）—— 800 元的赔付被计成了 %s",
			n, cnt, sum, sum)
	}
	if ok != 1 {
		t.Errorf("%d 个并发关闭里有 %d 个返回成功（应恰好 1 个）", n, ok)
	}
}

// waitForLockWaiter 等到有别的连接卡在行锁上。
//
// 用它代替 sleep：不管被卡住的是 SELECT … FOR UPDATE（修好之后）
// 还是那条不带状态条件的 UPDATE（修之前），都会出现在这里，
// 所以两种实现下这个同步点都成立，用例不靠时序碰运气。
func (e *testEnv) waitForLockWaiter(t *testing.T, self int32) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := e.pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> $1
			  AND wait_event_type = 'Lock' AND state = 'active'`, self).Scan(&n); err != nil {
			t.Fatalf("查等锁的连接失败：%v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等了 10 秒也没有连接卡在行锁上 —— 驳回可能压根没碰这一行")
}

// TestRejectLosingTheRaceDoesNotUndoApproval 审批和驳回同时发生，驳回不能推翻已生效的审批。
//
// 场景是真实的：财务两个人同时在处理同一笔报销，一个批一个驳。
// 审批那一步不可逆（应付 + 付款申请都已经落库），所以谁先落地谁算数，
// 后到的那个必须拿到 409，而不是把状态覆盖成"已驳回"——
// 那样账上就挂着一笔"已驳回"却仍要付的钱，对账时谁也说不清。
//
// 用持锁的事务把竞态钉死，不靠 sleep：
//  1. 用例开一个事务锁住这一行（状态仍是 submitted）
//  2. 发驳回请求，它必然卡在锁上
//  3. 用例把状态改成 approved（模拟审批先落地）并提交
//  4. 驳回解除阻塞——它现在看到的应该是 approved
func TestRejectLosingTheRaceDoesNotUndoApproval(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbNo := e.mkWaybillAt("arrived")
	rid := e.mkReimbursement(token, wbNo, "158.50")

	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开事务失败：%v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var self int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&self); err != nil {
		t.Fatalf("取连接号失败：%v", err)
	}
	var cur string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM fin_reimbursement WHERE id=$1::uuid FOR UPDATE`, rid).Scan(&cur); err != nil {
		t.Fatalf("锁行失败：%v", err)
	}
	if cur != "submitted" {
		t.Fatalf("造出来的报销状态是 %q，用例前提不成立", cur)
	}

	done := make(chan int, 1)
	go func() {
		done <- e.call(token, "POST", "/api/v1/finance/reimbursements/"+rid+"/reject",
			`{"reason":"并发用例：重复报销"}`).Code
	}()

	e.waitForLockWaiter(t, self)

	// 审批先落地
	if _, err := tx.Exec(ctx,
		`UPDATE fin_reimbursement SET status='approved', updated_at=now() WHERE id=$1::uuid`, rid); err != nil {
		t.Fatalf("模拟审批落地失败：%v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("提交失败：%v", err)
	}

	var code int
	select {
	case code = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("驳回请求解锁后 10 秒没返回")
	}

	var final string
	if err := e.pool.QueryRow(ctx,
		`SELECT status FROM fin_reimbursement WHERE id=$1::uuid`, rid).Scan(&final); err != nil {
		t.Fatalf("回读状态失败：%v", err)
	}
	if final != "approved" {
		t.Errorf("审批已经落地（应付和付款申请都开了），驳回却把状态改成了 %q —— "+
			"账上挂着一笔已驳回却仍要付的钱", final)
	}
	if code != http.StatusConflict {
		t.Errorf("抢输的驳回返回 %d，应该是 409：这不是成功，是「别人先批了」", code)
	}
}
