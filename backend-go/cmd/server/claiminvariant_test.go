package main

// 「调度中」与「谁锁的」这两列必须成对。
//
// 产品里 claim / release 永远同时写 status 和 claimed_by_id，所以这个不变式
// 现在是成立的。钉住它是因为一旦破了，症状特别隐蔽：
// 调度台「待分配」的谓词是 status IN ('pooled','dispatching') AND claimed_by_id IS NULL，
// 于是这种单**看得见**，点「锁定」却恒定 409「订单已被锁定或不在池中」——
// 而其实没有任何人锁着它。调度员会以为系统坏了，而日志里什么都没有。
//
// 这一条是造数踩出来的：bulk-load 和 seed 都只写了 status='dispatching'
// 不写锁定人，造出 4168 张这样的单（占「待分配」池的一半）。
// 产品自己产生不出这个状态，但没有任何东西拦着别人造出来。

import (
	"context"
	"testing"
)

func (e *testEnv) claimState(orderID string) (status string, claimed bool) {
	e.t.Helper()
	var by *string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status, claimed_by_id::text FROM ops_order WHERE id=$1::uuid`, orderID).
		Scan(&status, &by); err != nil {
		e.t.Fatalf("查订单失败：%v", err)
	}
	return status, by != nil
}

// TestClaimAndReleaseKeepStatusAndClaimerInSync 抢单和退回都要成对改这两列。
func TestClaimAndReleaseKeepStatusAndClaimerInSync(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	id := e.mkOrder() // pooled

	if st, c := e.claimState(id); st != "pooled" || c {
		t.Fatalf("初始状态不对：%s / 已锁定=%v", st, c)
	}
	if rec := e.call(token, "POST", "/api/v1/orders/"+id+"/claim", `{}`); rec.Code >= 300 {
		t.Fatalf("锁定返回 %d：%s", rec.Code, rec.Body.String())
	}
	if st, c := e.claimState(id); st != "dispatching" || !c {
		t.Errorf("锁定之后应是 dispatching + 有锁定人，实际 %s / 已锁定=%v —— "+
			"这两列不成对的单会在「待分配」里出现却锁不动", st, c)
	}
	if rec := e.call(token, "POST", "/api/v1/orders/"+id+"/release", `{}`); rec.Code >= 300 {
		t.Fatalf("退回返回 %d：%s", rec.Code, rec.Body.String())
	}
	if st, c := e.claimState(id); st != "pooled" || c {
		t.Errorf("退回之后应是 pooled + 无锁定人，实际 %s / 已锁定=%v", st, c)
	}
	// 退回之后必须能再次锁定——不然那张单就永远卡在池子里
	if rec := e.call(token, "POST", "/api/v1/orders/"+id+"/claim", `{}`); rec.Code >= 300 {
		t.Errorf("退回之后再锁定失败 %d：%s —— 单子会卡死在池子里", rec.Code, rec.Body.String())
	}
}

// TestSeedDataHasNoOrphanDispatching 演示/造量数据里不许有"调度中但没人锁"的单。
//
// 这一条守的不是产品逻辑，是**数据**：seed 与 bulk-load 都曾经只写状态不写锁定人。
// 拿这种库去演示或走查，调度台看起来就是坏的——而代码是对的。
func TestSeedDataHasNoOrphanDispatching(t *testing.T) {
	e := newTestEnv(t)
	var n int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM ops_order
		WHERE NOT is_deleted AND status='dispatching' AND claimed_by_id IS NULL`).Scan(&n); err != nil {
		t.Fatalf("统计失败：%v", err)
	}
	if n > 0 {
		t.Errorf("库里有 %d 张「调度中但没人锁」的订单。"+
			"它们会出现在调度台「待分配」里，而点「锁定」恒定 409——"+
			"看起来像产品坏了，其实是造数只写了状态。"+
			"检查 cmd/seed 与 scripts/dev/bulk-load.sh 是否成对写了 claimed_by_id。", n)
	}
}
