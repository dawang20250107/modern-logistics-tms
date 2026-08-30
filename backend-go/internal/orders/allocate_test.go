package orders

// 批量派车的应付分摊。这是**分钱**的代码：一批订单委托给同一个承运商，
// 一笔总应付要拆到每张单上。拆错了不会报错，只会让财务那边的数对不上。
//
// 三种口径：按吨占比 / 均摊 / 逐单指定，末单吸收舍入误差。
// 末单吸收是对的设计——先算前 N-1 单再让最后一单等于「总额减去已分出去的」，
// 这样各单之和恒等于总额，不会出现"分完了还差一分钱"。
// 下面第一组用例就是钉这条不变式的。

import (
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

func mkOrders(weights ...string) []dispatchableOrder {
	out := make([]dispatchableOrder, len(weights))
	for i, w := range weights {
		out[i] = dispatchableOrder{ID: string(rune('a' + i)), Weight: dec(w)}
	}
	return out
}

func sum(m map[string]decimal.Decimal) decimal.Decimal {
	t := decimal.Zero
	for _, v := range m {
		t = t.Add(v)
	}
	return t
}

// TestAllocateSumsToTotal 不变式：各单分摊之和 == 总应付，一分不差。
//
// 这是分钱代码唯一不能破的一条。挑的都是除不尽的数——
// 能整除的组合就算实现是错的也照样对得上，测不出东西。
func TestAllocateSumsToTotal(t *testing.T) {
	for _, c := range []struct {
		name    string
		total   string
		weights []string
		mode    string
	}{
		{"均摊·除不尽", "100.00", []string{"1", "1", "1"}, "average"},
		{"均摊·一分钱", "0.01", []string{"1", "1", "1"}, "average"},
		{"均摊·十单", "1000.00", []string{"1", "1", "1", "1", "1", "1", "1", "1", "1", "1"}, "average"},
		{"按吨·除不尽", "1000.00", []string{"3", "3", "3"}, "by_weight"},
		{"按吨·悬殊", "9999.99", []string{"0.01", "50", "0.02"}, "by_weight"},
		{"按吨·两位小数", "512.37", []string{"1.5", "2.25", "3.75"}, "by_weight"},
		{"单单一批", "88.88", []string{"5"}, "by_weight"},
	} {
		t.Run(c.name, func(t *testing.T) {
			total := dec(c.total)
			got := allocatePayable(total, mkOrders(c.weights...), c.mode, nil)
			if s := sum(got); !s.Equal(total) {
				t.Errorf("分摊之和 %s ≠ 总额 %s（差 %s）", s, total, s.Sub(total))
			}
			if len(got) != len(c.weights) {
				t.Errorf("分了 %d 单，应为 %d 单", len(got), len(c.weights))
			}
		})
	}
}

// TestAllocateByWeightIsProportional 按吨分摊要真的按吨。
func TestAllocateByWeightIsProportional(t *testing.T) {
	got := allocatePayable(dec("1000"), mkOrders("1", "4"), "by_weight", nil)
	// 1:4 → 200 / 800
	if !got["a"].Equal(dec("200")) {
		t.Errorf("轻单分到 %s，按 1:4 应为 200", got["a"])
	}
	if !got["b"].Equal(dec("800")) {
		t.Errorf("重单分到 %s，按 1:4 应为 800", got["b"])
	}
}

// TestAllocateZeroWeightFallsBackToAverage 全部零吨位时按吨分摊要退回均摊，
// 而不是除以零。零吨位是真会出现的：录单时没填重量。
func TestAllocateZeroWeightFallsBackToAverage(t *testing.T) {
	got := allocatePayable(dec("300"), mkOrders("0", "0", "0"), "by_weight", nil)
	if s := sum(got); !s.Equal(dec("300")) {
		t.Errorf("零吨位时分摊之和 %s ≠ 300", s)
	}
	for id, v := range got {
		if !v.Equal(dec("100")) {
			t.Errorf("%s 分到 %s，零吨位应退回均摊 100", id, v)
		}
	}
}

// TestAllocateNeverNegative 任何一单的应付都不能是负数。
//
// 末单吸收舍入误差的写法有个边界：前 N-1 单各自 Round(2) 都往上舍入时，
// 已分出去的可能**超过**总额，末单于是变成负数——一张"应付 -0.09 元"的单，
// 会一路流进财务对账。
// 触发条件是总额小、单数多（总额/单数 落在 0.005 附近）。
// 现实里多半是录单把 1000 敲成了 1，但系统不该把它变成负应付。
func TestAllocateNeverNegative(t *testing.T) {
	for _, c := range []struct {
		total string
		n     int
	}{
		{"0.10", 20},
		{"0.19", 20},
		{"0.44", 10},
		{"1.00", 100},
		{"0.03", 7},
	} {
		t.Run(c.total+"/"+string(rune('0'+c.n/10))+string(rune('0'+c.n%10)), func(t *testing.T) {
			w := make([]string, c.n)
			for i := range w {
				w[i] = "1"
			}
			got := allocatePayable(dec(c.total), mkOrders(w...), "average", nil)
			for id, v := range got {
				if v.IsNegative() {
					t.Errorf("总额 %s 分 %d 单时，%s 分到负数 %s —— "+
						"末单吸收舍入误差的写法在总额小、单数多时会翻负",
						c.total, c.n, id, v)
				}
			}
			if s := sum(got); !s.Equal(dec(c.total)) {
				t.Errorf("分摊之和 %s ≠ %s", s, c.total)
			}
		})
	}
}

// TestAllocateManualIsTakenVerbatim 逐单指定：当前实现原样采纳，不校验合计。
//
// 这条**记录现状**，不是主张现状是对的：
// 调度员把 3 张单各填 1000，而批次总应付是 2000 时，
// 系统会照单全收，批次上记着 2000、三张单上加起来 3000。
// 两个数从此各说各话，而没有任何一处会报错。
//
// 另外 manual 里缺项或填了非数字时，decimal.NewFromString 的错误被忽略，
// 那一单静默变成 0——一张本该有应付的单变成 0 元，同样不报错。
//
// 要不要拦、拦了之后回 400 还是自动按比例缩放，是业务决定，
// 所以这里只把行为钉住：将来谁改了这个语义，这条用例会红，
// 那时候是有意改的还是手滑，就说得清了。
func TestAllocateManualIsTakenVerbatim(t *testing.T) {
	orders := mkOrders("1", "1", "1")
	got := allocatePayable(dec("2000"), orders, "manual", map[string]string{
		"a": "1000", "b": "1000", "c": "1000",
	})
	if s := sum(got); !s.Equal(dec("3000")) {
		t.Errorf("逐单指定合计 %s，当前实现应原样采纳为 3000", s)
	}
	if s := sum(got); s.Equal(dec("2000")) {
		t.Log("合计被规整到了总额——语义变了，确认是有意的再改这条用例")
	}

	// 缺项 / 非数字 → 静默为 0
	got2 := allocatePayable(dec("2000"), orders, "manual", map[string]string{
		"a": "1000", "b": "不是数字",
	})
	if !got2["b"].IsZero() {
		t.Errorf("非数字的手工值应为 0（现状），实得 %s", got2["b"])
	}
	if !got2["c"].IsZero() {
		t.Errorf("缺项的手工值应为 0（现状），实得 %s", got2["c"])
	}
}

// TestAllocateNonPositiveTotal 总额为 0 或负时，每单都是 0，不能出现负分摊。
func TestAllocateNonPositiveTotal(t *testing.T) {
	for _, total := range []string{"0", "-100"} {
		got := allocatePayable(dec(total), mkOrders("1", "2"), "by_weight", nil)
		for id, v := range got {
			if !v.IsZero() {
				t.Errorf("总额 %s 时 %s 分到 %s，应为 0", total, id, v)
			}
		}
	}
}

// TestAllocateFairnessWithinOneCent 最大余数法的第三条性质：
// 同等条件下任意两单的偏差不超过一分钱。
//
// 末单吸收法没有这条：总额 0.44 分 10 单时，前 9 单各 0.04、末单 0.08——
// 末单是别人的两倍。分摊之和虽然仍然等于总额（那条不变式没破），
// 但"为什么最后这张单贵一倍"是要向客户解释的。
func TestAllocateFairnessWithinOneCent(t *testing.T) {
	for _, c := range []struct {
		total string
		n     int
	}{
		{"0.44", 10}, {"100.00", 3}, {"1000.00", 7}, {"0.10", 20},
	} {
		w := make([]string, c.n)
		for i := range w {
			w[i] = "1"
		}
		got := allocatePayable(dec(c.total), mkOrders(w...), "average", nil)
		lo, hi := decimal.NewFromInt(1<<40), decimal.NewFromInt(-1)
		for _, v := range got {
			if v.LessThan(lo) {
				lo = v
			}
			if v.GreaterThan(hi) {
				hi = v
			}
		}
		if spread := hi.Sub(lo); spread.GreaterThan(dec("0.01")) {
			t.Errorf("总额 %s 分 %d 单，最大 %s 最小 %s，相差 %s > 0.01",
				c.total, c.n, hi, lo, spread)
		}
	}
}

// TestAllocateIsDeterministic 同样的输入必须永远得到同样的结果。
//
// 最大余数法要对小数部分排序，而 Go 的 map 遍历顺序是随机的：
// 如果排序键平手时不按订单顺序兜底，同一批单重跑一次分摊结果就可能不同。
// 分钱的函数有不确定性，意味着重跑一次对账就对不上。
func TestAllocateIsDeterministic(t *testing.T) {
	orders := mkOrders("1", "1", "1", "1", "1", "1", "1")
	first := allocatePayable(dec("1.00"), orders, "average", nil)
	for i := 0; i < 50; i++ {
		again := allocatePayable(dec("1.00"), orders, "average", nil)
		for id, v := range first {
			if !again[id].Equal(v) {
				t.Fatalf("第 %d 次重跑，%s 从 %s 变成了 %s —— 分摊不可复现",
					i+1, id, v, again[id])
			}
		}
	}
}
