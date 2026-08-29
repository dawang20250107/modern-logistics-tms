package main

// 抢单：两个调度同时点同一张订单。
//
// 这是这套系统里**天生就并发**的那个动作——订单池是所有调度共用的一块屏，
// 谁先点谁拿到。两个人同时点必须只有一个成功；两个都成功的话，
// 两个人会各自去派车，同一批货被派两次。
//
// 现在的实现是对的（事务里 SELECT ... FOR UPDATE 之后再判 claimed_by），
// 这条用例是把它钉住：这种守卫在重构里最容易丢——去掉 FOR UPDATE
// 之后一切照常，只有在真并发下才出错，而那时是生产环境。

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

func TestConcurrentClaimOnlyOneWins(t *testing.T) {
	e := newTestEnv(t)
	a := e.mkUser(true)
	b := e.mkUser(true)
	orderID := e.mkOrder() // mkOrder 建出来就是 pooled

	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := a
			if i%2 == 1 {
				tok = b
			}
			<-start // 尽量同时发出去
			codes[i] = e.call(tok, "POST", "/api/v1/orders/"+orderID+"/claim", `{}`).Code
		}(i)
	}
	close(start)
	wg.Wait()

	won := 0
	for _, c := range codes {
		switch {
		case c < 300:
			won++
		case c == http.StatusConflict:
			// 预期：没抢到
		default:
			t.Errorf("既不是成功也不是 409，而是 %d —— 并发下出了别的错", c)
		}
	}
	if won != 1 {
		t.Errorf("%d 个并发抢单里有 %d 个成功（应恰好 1 个）—— "+
			"同一批货会被派两次", n, won)
	}

	// 库里也必须只有一个锁定人
	var claimed *string
	var status string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT claimed_by_id::text, status FROM ops_order WHERE id=$1::uuid`, orderID).
		Scan(&claimed, &status); err != nil {
		t.Fatalf("查订单失败：%v", err)
	}
	if claimed == nil {
		t.Error("抢完之后订单没有锁定人")
	}
	if status != "dispatching" {
		t.Errorf("抢完之后订单状态=%q，应为 dispatching", status)
	}
}
