package orders

// 公开下单表单填错数字：该回 400 和"哪一格"，不该回 500 和一段 SQL。
//
// 实测过的原状（匿名调用，重量填「三吨」）：
//   HTTP 500  建单失败：ERROR: invalid input syntax for type numeric: "三吨" (SQLSTATE 22P02)
// 而页面上写的是「提交失败，请检查网络后重试」——问题跟网络毫无关系，
// 客户照着这句话重试一万次，结果一样。
//
// 这条只验归一化函数本身（不需要库）：三种输入形态 × 认不认。

import (
	"encoding/json"
	"testing"
)

func TestNumOrErrRejectsNonNumbersInsteadOfLettingThemReachSQL(t *testing.T) {
	ok := []struct {
		in   any
		want float64
	}{
		{nil, 0},
		{"", 0},
		{"  ", 0},
		{"3", 3},
		{"3.5", 3.5},
		{float64(7), 7},
		{json.Number("12.25"), 0}, // json.Number 原样透传，由 SQL 层解析
	}
	for _, c := range ok {
		v, bad := numOrErr(c.in, "重量(吨)")
		if bad != "" {
			t.Errorf("numOrErr(%#v) 判成了非法，实际应当放行", c.in)
			continue
		}
		if f, isF := v.(float64); isF && f != c.want && c.want != 0 {
			t.Errorf("numOrErr(%#v) = %v，期望 %v", c.in, f, c.want)
		}
	}

	// 这些必须被拦住——放过去就会一路传到 INSERT，让 Postgres 去报错，
	// 而那条报错会带着引擎、列类型和 SQLSTATE 回给匿名调用方。
	for _, bad := range []any{"三吨", "3吨", "abc", "3.5.6", true, []any{1}, map[string]any{}} {
		if _, field := numOrErr(bad, "重量(吨)"); field == "" {
			t.Errorf("numOrErr(%#v) 放行了——它会变成一句 SQL 报错发给客户", bad)
		}
	}
}
