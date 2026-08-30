// Package expitem 费用科目词表的唯一出处。
//
// 为什么要单独一个包：这张表原先有两份拷贝（waybills/cards.go 与
// finance/dashboard.go），而**第三个地方压根没有校验**——计价规则的
// expense_item_code 是自由文本。前端计价规则表单又把它写死成 "FREIGHT"，
// 而 FREIGHT 不在任何一份词表里。
//
// 后果不是报错，是**钱落到一个谁也不认识的科目里**：
// 计价规则算出来的费用照样入账，但财务看板按科目分组时它落进一个没有名字的桶，
// 对账单行上显示的也是 FREIGHT 这个原始码。手工录费用那条路径校验得很严
// （waybills.AddExpense 会比对词表），规则生成这条路径一点都不校验——
// 同一个字段两套标准，宽的那套决定了实际数据的质量。
//
// 运单状态词表此前有 8 份拷贝，收成 1 份之后才发现有几处早就对不上了。
// 这张表现在收在这里，加科目只改这一个文件。
package expitem

// Cost 成本科目（应付方向）
var Cost = map[string]string{
	"TRANSPORT_COST": "运费", "FUEL_CARD": "油卡", "TOLL": "过路费", "LOADING": "装卸费",
	"DETENTION": "押车费", "INFO_FEE": "信息费", "RECEIPT_FEE": "回单费", "DEDUCTION": "扣款",
	"EXCEPTION_COST": "异常费用", "OTHER_COST": "其他成本",
}

// Income 收入科目（应收方向）
var Income = map[string]string{
	"TRANSPORT_INCOME": "运费收入", "SURCHARGE": "附加费", "INSURANCE": "保险费",
	"WAITING_FEE": "等候费", "OTHER_INCOME": "其他收入",
}

// Payees 收付款方类型
var Payees = map[string]string{
	"carrier": "承运商", "driver": "司机", "fuel_card": "油卡商", "customer": "客户", "other": "其他",
}

// Label 科目中文名。认不出来时原样返回码，不编一个名字出来——
// 界面上看到一个大写英文码，比看到一个像模像样但错的中文名要好查得多。
func Label(code string) string {
	if v, ok := Cost[code]; ok {
		return v
	}
	if v, ok := Income[code]; ok {
		return v
	}
	return code
}

// IsCost / IsIncome / Valid 校验用
func IsCost(code string) bool   { _, ok := Cost[code]; return ok }
func IsIncome(code string) bool { _, ok := Income[code]; return ok }
func Valid(code string) bool    { return IsCost(code) || IsIncome(code) }

// AllCodes 两个方向的全部科目码（顺序稳定，供 CRUD 引擎做枚举校验）。
//
// 计价规则用的是这个合集而不是按方向分开的两个集合：规则的方向由
// price_type 决定，而通用 CRUD 引擎的枚举校验看不到同一请求里的另一个字段。
// 合集已经足够挡住 "FREIGHT" 这类根本不存在的码；方向该配哪一批由界面来分。
func AllCodes() []string {
	out := make([]string, 0, len(Cost)+len(Income))
	for _, c := range order(Cost) {
		out = append(out, c)
	}
	for _, c := range order(Income) {
		out = append(out, c)
	}
	return out
}

func order(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// 排序只为让错误消息里的可选值稳定，不影响语义
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
