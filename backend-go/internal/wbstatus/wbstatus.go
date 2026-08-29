// Package wbstatus 运单状态在**统计口径**上的唯一定义。
//
// 存在的理由是一次实测出来的错账：
//
// 运单状态机里，departed / in_transit 没有通往 cancelled / voided 的边
// （实测 POST transition 回 409）。也就是说，车已经发了之后要作废这一单，
// 系统允许的唯一路径是 departed → in_transit → arrived → rejected → cancelled——
// 必须先记一次**没有发生过的到达**。而 arrived 会写 arrived_at 里程碑。
//
// 承运商评分卡那三处的准班率按 `arrived_at IS NOT NULL` 取样，
// 状态上只排除了 voided、没排除 cancelled。于是那张被取消的运单
// 既进分母又进分子——一单根本没送到的货，算成了一次准点交付。
// 实测：取消前 分母=0 分子=0，走完上面那条路径后 分母=1 分子=1，
// 该承运商准班率 100%。
//
// 而 analytics 里的两处（ops.on_time_rate 的定义与看板）本来就是对的，
// 它们按 status IN ('arrived','signed','delivered','settled') 取样。
// 所以这不是要新定一套口径，是让跑偏的三处回到已有的那套——
// 同一个承运商在看板上和评分卡上给出两个准班率，比给错一个更伤信任。
package wbstatus

import (
	"sort"
	"strings"
)

// Delivered 已实际完成交付动作的运单状态。
//
// 统计"送到了没有""准不准点"这类指标时，样本必须从这里取：
//   - cancelled / voided 没有送达，不该出现在准班率的任何一侧
//   - rejected（到了但被拒收）也不计入：口径与 analytics 的 ops.on_time_rate 对齐。
//     代价是承运商的准点表现被低估了一点（车确实按时到了，是货被拒的），
//     但拒收本身已由异常率单独反映；两套口径不一致的代价更大。
var Delivered = []string{"arrived", "signed", "delivered", "settled"}

// DeliveredSQL 供拼进 SQL 的字面量，形如 ('arrived','signed',…)。
// 用常量而不是占位符，是为了能直接嵌进已有的大段 CTE 而不打乱参数序号。
const DeliveredSQL = `('arrived','signed','delivered','settled')`

// Aborted 中止：车已经发出去了，但这一趟不会送到了。
//
// 加这个状态之前，departed / in_transit 没有任何出口——想作废一张已发车的运单，
// 系统允许的唯一路径是 departed → in_transit → arrived → rejected → cancelled，
// 必须先记一次**没有发生过的到达**，那个假 arrived_at 会永久留在运单时间线上。
//
// 为什么不复用已有的两个：
//
//	· voided（作废）= 当它没发生过。承运商统计整个排除它，split/merge 的原单
//	  也用它。但车确实开出去过、人和车确实被占用过，说"没发生过"是在抹掉事实。
//	· cancelled（取消）= 业务被取消，车还没走。
//	· aborted（中止）= 走了但没送到。几乎总有钱要算：空驶费、半程运费、货损。
//
// 所以 aborted 的唯一出口是 settled——**中止必须过一次结算**。
// 不给它直连终态，是因为中止基本都有费用争议，让它悄悄消失才是坏设计；
// 没有费用时结算 0 元也是一次明确的确认。
const Aborted = "aborted"

// Label 运单状态的中文标签。**全仓库唯一一份。**
//
// 收拢之前这张表在后端有 5 份一模一样的拷贝
// （driver / orders.workflow / orders.public / waybills.cards / analytics.handler），
// 加一个状态要改 5 处，漏掉任何一处的表现是「界面上显示成原始英文码」——
// 不报错，只是看起来像没翻译。
var Label = map[string]string{
	"draft":            "草稿",
	"pending_dispatch": "待调度",
	"dispatched":       "已派车",
	"loaded":           "已装车",
	"departed":         "已发车",
	"in_transit":       "运输中",
	"arrived":          "已到达",
	"partially_signed": "部分签收",
	"rejected":         "已拒收",
	"signed":           "已签收",
	"delivered":        "已送达",
	"settled":          "已结算",
	"cancelled":        "已取消",
	"voided":           "已作废",
	Aborted:            "已中止",
}

// LabelOf 取标签；未知状态原样返回，便于排查而不是显示成空白。
func LabelOf(s string) string {
	if v, ok := Label[s]; ok {
		return v
	}
	return s
}

// LabelCaseSQL 生成 SQL 里的 CASE 表达式，供那些在库里拼标签的查询用。
// col 形如 "w.status"。同样是为了不再出现第二份状态词表。
func LabelCaseSQL(col string) string {
	var b strings.Builder
	b.WriteString("(CASE " + col)
	// 固定顺序输出，保证生成的 SQL 稳定（map 遍历是随机的，
	// 不排序的话每次启动拼出来的语句都不一样，查询计划缓存会失效）
	keys := make([]string, 0, len(Label))
	for k := range Label {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(" WHEN '" + k + "' THEN '" + Label[k] + "'")
	}
	b.WriteString(" ELSE " + col + " END)")
	return b.String()
}
