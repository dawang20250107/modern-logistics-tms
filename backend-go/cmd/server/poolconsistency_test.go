package main

// 调度台上每个页签的计数，必须等于点进去那个列表的 total。
//
// 这是调度员每天照着做决定的三个数字。它们分别来自两个端点
// （/orders/pool-counts 和 /orders/pool），历史上出过一次事故：
// 计数说 8336、点进去只有 20 条——因为计数走服务端全量、
// 列表只拉了一页，而"仅看紧急""搜索"全在前端对着那一页做。
//
// 那次的修法是让两边**共用同一份 where 构造器**（poolWhere / dispatchedWhere）。
// 共用这件事在代码里看得见，但看不见的是"有没有人哪天给其中一边多加了个条件"。
// 这条用例从外面按：两个端点各打一次，数字必须相等。
//
// 也顺带钉住三个 scope 的含义，它们不是同义词：
//   free   未认领未指派的（待分配页签）
//   mine   本人认领或被指派的（可调派页签）
//   all    数据范围内全部（仅超管/全局范围有效，否则退回 mine）
// 不传 scope 时不加收窄——含义是"数据范围内的全部池中单"，
// 界面上每个页签都显式传，所以走不到这一档；这里一并钉住，
// 免得哪天有人把它当成 free 的同义词。

import (
	"encoding/json"
	"net/http"
	"testing"
)

func (e *testEnv) poolTotal(token, path string) int {
	e.t.Helper()
	rec := e.call(token, "GET", path, "")
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET %s 返回 %d：%s", path, rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析 %s 失败：%v", path, err)
	}
	return out.Data.Total
}

func (e *testEnv) poolCounts(token, scope string) map[string]int {
	e.t.Helper()
	rec := e.call(token, "GET", "/api/v1/orders/pool-counts?scope="+scope, "")
	if rec.Code != http.StatusOK {
		e.t.Fatalf("pool-counts 返回 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析 pool-counts 失败：%v", err)
	}
	return out.Data
}

func TestPoolTabCountsMatchTheirLists(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true) // 超管：scope=all 才有意义

	counts := e.poolCounts(token, "all")
	// 前提自检：库里得真有池中单，否则"两边都是 0"什么都没测到
	if counts["unassigned"]+counts["dispatchable"]+counts["dispatched"] == 0 {
		t.Skip("库里三个池都是空的，这条用例测不到东西")
	}

	for _, c := range []struct{ key, path, tab string }{
		{"unassigned", "/api/v1/orders/pool?scope=free&page_size=1", "待分配"},
		{"dispatchable", "/api/v1/orders/pool?scope=all&page_size=1", "可调派"},
		{"dispatched", "/api/v1/orders/dispatched?scope=all&page_size=1", "已调派"},
	} {
		got := e.poolTotal(token, c.path)
		if got != counts[c.key] {
			t.Errorf("「%s」页签：计数端点说 %d，列表 total 是 %d —— 两边的谓词不同步了。\n"+
				"  调度员照着这个数决定今天先派哪一批；数字对不上时他不会知道该信哪个。",
				c.tab, counts[c.key], got)
		}
	}
	t.Logf("三个页签：待分配 %d、可调派 %d、已调派 %d",
		counts["unassigned"], counts["dispatchable"], counts["dispatched"])
}

// TestPoolScopesAreNotSynonyms 三个 scope 的含义不能互相塌陷。
func TestPoolScopesAreNotSynonyms(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)

	free := e.poolTotal(token, "/api/v1/orders/pool?scope=free&page_size=1")
	all := e.poolTotal(token, "/api/v1/orders/pool?scope=all&page_size=1")
	none := e.poolTotal(token, "/api/v1/orders/pool?page_size=1")

	if free == 0 && all == 0 {
		t.Skip("池是空的，测不到东西")
	}
	// free 是 all 的真子集：未认领的一定也在"全部池中单"里
	if free > all {
		t.Errorf("scope=free（%d）比 scope=all（%d）还多 —— free 应是 all 的子集", free, all)
	}
	// 不传 scope = 不加收窄，对超管来说和 all 同量
	if none != all {
		t.Errorf("不传 scope 得到 %d，scope=all 得到 %d —— "+
			"不传时的含义是「数据范围内全部池中单」，对超管应与 all 相同", none, all)
	}
	// 演示库里两者必须真的不同，否则这条用例在一个退化的库上恒绿
	if free == all {
		t.Skipf("这个库里 free 与 all 恰好相等（都是 %d）—— "+
			"没有被人认领的单，区分不出来，跳过", free)
	}
}
