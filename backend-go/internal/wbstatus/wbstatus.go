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
