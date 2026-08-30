package waybills

// 运单状态机的结构性不变式。
//
// 这张表（allowedTransitions）是运单能怎么走的唯一定义，十几个状态、
// 三十来条边，全靠手写。手写的状态表有几类错，代码审查很难看出来：
//   · 目标状态名拼错 → 造出一个永远到不了的死状态，而且只在跑到那条边时才 404
//   · 某个状态走不到终态 → 运单卡在中间，永远结不了案
//   · 某个状态从初态不可达 → 一段永远执行不到的代码
// 这些都是一遍图遍历就能查出来的，不该靠人盯。
//
// 另外还钉了两处**跨定义一致性**：签收的合法起点、里程碑字段的键，
// 都必须落在这张表认识的状态里。两处定义各自演化是这套系统反复出过的问题。

import (
	"testing"

	"github.com/shopspring/decimal"
)

func allStates() map[string]bool {
	s := map[string]bool{}
	for k := range allowedTransitions {
		s[k] = true
	}
	return s
}

// TestTransitionTargetsAreDefined 每条边的目标状态都必须在表里有定义。
//
// 拼错一个字母（比如 "canceled" 少个 l）不会编译报错，
// 只会造出一个进得去、出不来的状态——而且只有当真有运单走到那一步时才发作。
func TestTransitionTargetsAreDefined(t *testing.T) {
	states := allStates()
	for from, tos := range allowedTransitions {
		for _, to := range tos {
			if !states[to] {
				t.Errorf("%s → %s：目标状态 %q 在表里没有定义，"+
					"运单进去之后出不来（多半是拼错了）", from, to, to)
			}
		}
	}
}

// intendedTerminals 运单可以合法停在哪里。**写死在用例里**，不从表里推。
//
// 第一版是从表里推的（出边为空的就算终态），结果自己把自己废了：
// 把 delivered 的出边删掉做变异验证时，用例反而认为 delivered 成了终态，
// 于是绿灯放行——而那正是要抓的 bug（运单卡在"已交付"永远结不了账）。
// 判据要是从被判的数据里推出来的，就等于没有判据。
var intendedTerminals = map[string]bool{
	"settled": true, "cancelled": true, "voided": true,
}

// TestTerminalsAreExactlyTheIntendedOnes 出边为空的状态，必须**恰好**是那三个。
//
// 多出来一个 = 有状态成了死胡同，运单进去就结不了案；
// 少一个 = 某个终态被人加了出边，"已结算"还能再往下走。
func TestTerminalsAreExactlyTheIntendedOnes(t *testing.T) {
	for s, tos := range allowedTransitions {
		dead := len(tos) == 0
		switch {
		case dead && !intendedTerminals[s]:
			t.Errorf("%s 没有任何出边，成了死胡同——运单进了这个状态就结不了案。"+
				"若确实要新增一个终态，请一并加进 intendedTerminals", s)
		case !dead && intendedTerminals[s]:
			t.Errorf("%s 是终态却还有出边 %v——已经终结的单不该还能往下走", s, tos)
		}
	}
	for s := range intendedTerminals {
		if _, ok := allowedTransitions[s]; !ok {
			t.Errorf("终态 %s 在状态表里没有定义", s)
		}
	}
}

// TestEveryStateCanReachTerminal 任何状态都要能走到那三个终态之一。
//
// 走不到意味着运单永远结不了案：既不能结算，也不能作废，
// 在列表里挂着，而没有任何操作能让它离开。
func TestEveryStateCanReachTerminal(t *testing.T) {
	for start := range allowedTransitions {
		seen := map[string]bool{start: true}
		queue := []string{start}
		reached := false
		for len(queue) > 0 && !reached {
			cur := queue[0]
			queue = queue[1:]
			if intendedTerminals[cur] {
				reached = true
				break
			}
			for _, n := range allowedTransitions[cur] {
				if !seen[n] {
					seen[n] = true
					queue = append(queue, n)
				}
			}
		}
		if !reached {
			t.Errorf("%s 走不到 settled / cancelled / voided 中的任何一个——"+
				"进了这个状态的运单永远结不了案", start)
		}
	}
}

// TestEveryStateReachableFromDraft 每个状态都要能从初态走到。
// 到不了的状态意味着有一段永远执行不到的代码，多半是漏了一条边。
func TestEveryStateReachableFromDraft(t *testing.T) {
	seen := map[string]bool{"draft": true}
	queue := []string{"draft"}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range allowedTransitions[cur] {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	for s := range allowedTransitions {
		if !seen[s] {
			t.Errorf("%s 从 draft 走不到——要么漏了一条边，要么这个状态已经废弃了", s)
		}
	}
}

// TestNoSelfTransition 不允许原地打转的边。
func TestNoSelfTransition(t *testing.T) {
	for from, tos := range allowedTransitions {
		for _, to := range tos {
			if from == to {
				t.Errorf("%s → 自己：这条边没有意义，而且会让"+
					"「下一步能去哪」的提示里出现当前状态", from)
			}
		}
	}
}

// TestSignableStatesCanReachSigned 签收的合法起点，必须真的能走到签收态。
//
// signableFrom 与 allowedTransitions 是两处独立的定义。
// in_transit 就是个例子：它在 signableFrom 里，但表里 in_transit 只能去 arrived——
// 代码里靠「签收回传自动到达」先补一跳把这个缺口接上了。
// 这条用例保证的是：将来谁往 signableFrom 里加状态时，
// 如果表里没有对应的路，会在这里红，而不是等到线上签不了单。
func TestSignableStatesCanReachSigned(t *testing.T) {
	targets := map[string]bool{"signed": true, "partially_signed": true}
	for s := range allowedTransitions {
		if !signableFrom(s) {
			continue
		}
		seen := map[string]bool{s: true}
		queue := []string{s}
		okPath := false
		for len(queue) > 0 && !okPath {
			cur := queue[0]
			queue = queue[1:]
			for _, n := range allowedTransitions[cur] {
				if targets[n] {
					okPath = true
					break
				}
				if !seen[n] {
					seen[n] = true
					queue = append(queue, n)
				}
			}
		}
		if !okPath {
			t.Errorf("%s 被 signableFrom 认作可签收起点，但状态表里从它走不到"+
				"signed / partially_signed —— 两处定义对不上", s)
		}
	}
}

// TestMilestoneFieldsAreKnownStates 里程碑字段的键必须是表里认识的状态。
// 拼错的话那个时间戳永远不会被写，而"到达时间一直是空的"很难联想到是键写错了。
func TestMilestoneFieldsAreKnownStates(t *testing.T) {
	states := allStates()
	for st, col := range milestoneField {
		if !states[st] {
			t.Errorf("里程碑 %q → %s：%q 不是表里的状态，这个时间戳永远不会被写", st, col, st)
		}
	}
}

// TestAlreadySignedStatesAreAtOrPastSigning alreadySigned 认作"已签"的状态，
// 不能同时又是 signableFrom 认作"可签"的——否则同一张单既算签过又能再签一次。
func TestAlreadySignedStatesAreAtOrPastSigning(t *testing.T) {
	for s := range allowedTransitions {
		if alreadySigned(s) && signableFrom(s) {
			t.Errorf("%s 同时满足 alreadySigned 与 signableFrom —— "+
				"这张单既算签过了、又还能再签一次", s)
		}
	}
}

func TestToDecimal(t *testing.T) {
	for _, c := range []struct {
		name string
		in   any
		want string
		err  bool
	}{
		{"nil → 0", nil, "0", false},
		{"空串 → 0", "", "0", false},
		{"全空白 → 0", "   ", "0", false},
		{"数字串", "12.34", "12.34", false},
		{"负数串", "-5", "-5", false},
		{"float64", float64(3.5), "3.5", false},
		{"整型 float64", float64(7), "7", false},
		// JSON 里的数字都是 float64，int 走不到——但真传进来要报错而不是静默 0，
		// 静默 0 在算钱的地方就是把金额吞了
		{"int 不接受", 7, "", true},
		{"非数字串报错", "abc", "", true},
		{"布尔不接受", true, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := toDecimal(c.in)
			if c.err {
				if err == nil {
					t.Errorf("期望报错，实得 %s —— 静默当 0 会把金额吞掉", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外报错：%v", err)
			}
			if !got.Equal(decimal.RequireFromString(c.want)) {
				t.Errorf("得到 %s，期望 %s", got, c.want)
			}
		})
	}
}
