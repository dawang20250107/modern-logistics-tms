// Package filters 服务端通用筛选：前端 FilterBuilder 的条件模型（filter=<JSON>）→ SQL WHERE 片段。
// 与 Django 侧 apps/core/filtering.py 逐运算符对齐，是各资源列表移植的共用地基。
//
// 模型：{"combinator":"and"|"or","conditions":[{"field","op","value"},...]}
// 每个资源声明 map[key]FilterField{Type, Cols}，Cols 为一个或多个 SQL 列表达式
// （多列用于「线路=起+讫」这类跨列文本，任一列命中即算命中）。
package filters

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type FieldType string

const (
	Text   FieldType = "text"
	Enum   FieldType = "enum"
	Number FieldType = "number"
	Date   FieldType = "date"
	Bool   FieldType = "bool"
)

type FilterField struct {
	Type FieldType
	Cols []string // SQL 列表达式（已含表别名），如 "o.order_no"、"c.name"
}

type condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

type model struct {
	Combinator string      `json:"combinator"`
	Conditions []condition `json:"conditions"`
}

// Args 收集查询参数并生成 $N 占位符（与调用方已有参数续接）。
type Args struct{ Values []any }

func (a *Args) Add(v any) string {
	a.Values = append(a.Values, v)
	return fmt.Sprintf("$%d", len(a.Values))
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func pair(v any) (any, any) {
	if arr, ok := v.([]any); ok && len(arr) >= 2 {
		return arr[0], arr[1]
	}
	return "", ""
}

func anyCol(cols []string, tmpl string, ph string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = fmt.Sprintf(tmpl, c, ph)
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// localDate 与 Django __date lookup 的时区语义对齐（TIME_ZONE=Asia/Shanghai）。
func localDate(col string) string {
	return "(" + col + " AT TIME ZONE 'Asia/Shanghai')::date"
}

// BadValue 用户填进筛选框的值不是这个字段该有的形状。
//
// 由来：日期筛选的值最终作为 $N 传给 Postgres 的 ::date。参数化挡住了注入，
// 但**挡不住"这压根不是个日期"**——用户在日期框里打「今天」、
// 或者手改地址栏写成 2026-13-45，Postgres 直接报错，
// 整个请求变成 500「查询失败」。
//
// 而 500 的含义是"服务端出故障了"：它会进错误率、会触发告警、
// 会让人去查网关和数据库。这里没有任何故障，只是有人填了个不是日期的东西。
//
// 也不能默默忽略这个条件：那样用户看到的是**没有筛过的全量**，
// 而界面上那个筛选条件还亮着——这套系统在"安静地把全量说成一部分"
// 上栽过好几次，反过来同样不能接受。
// 所以明确报错，由调用方回 400 并说清是哪个字段。
type BadValue struct {
	Field string
	Value string
}

func (e *BadValue) Error() string {
	return "筛选条件「" + e.Field + "」的值不是合法日期：" + e.Value
}

// isDate 只接受 Postgres 稳定认得的那几种写法。
// 不用 time.Parse 一种格式了事：前端发的是 YYYY-MM-DD，
// 而手写的地址栏里 2026/01/01 也是能用的，不该被判死。
func isDate(s string) bool {
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006-1-2", "2006/1/2"} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// checkValues 提交给 SQL 之前先看一眼形状。目前只有日期需要——
// 数字走 toFloat（解不开就整条忽略，不会到 SQL），枚举/文本都是字符串。
func checkValues(f FilterField, name, op string, value any) error {
	if f.Type != Date {
		return nil
	}
	check := func(v any) error {
		s := fmt.Sprint(v)
		if s == "" || isDate(s) {
			return nil
		}
		return &BadValue{Field: name, Value: s}
	}
	if op == "between" {
		lo, hi := pair(value)
		if err := check(lo); err != nil {
			return err
		}
		return check(hi)
	}
	return check(value)
}

func condSQL(f FilterField, op string, value any, args *Args) string {
	if len(f.Cols) == 0 {
		return ""
	}
	switch f.Type {
	case Text:
		if op == "empty" || op == "nempty" {
			parts := make([]string, len(f.Cols))
			for i, c := range f.Cols {
				parts[i] = fmt.Sprintf("(COALESCE(%s,'') = '')", c)
			}
			blank := "(" + strings.Join(parts, " AND ") + ")"
			if op == "empty" {
				return blank
			}
			return "NOT " + blank
		}
		v, _ := value.(string)
		if v == "" {
			return ""
		}
		switch op {
		case "contains":
			return anyCol(f.Cols, "%s ILIKE %s", args.Add("%"+v+"%"))
		case "ncontains":
			return "NOT " + anyCol(f.Cols, "%s ILIKE %s", args.Add("%"+v+"%"))
		case "eq":
			return anyCol(f.Cols, "LOWER(%s) = LOWER(%s)", args.Add(v))
		case "neq":
			return "NOT " + anyCol(f.Cols, "LOWER(%s) = LOWER(%s)", args.Add(v))
		}
		return ""

	case Enum, Bool:
		var vals []string
		switch x := value.(type) {
		case []any:
			for _, e := range x {
				if s := fmt.Sprint(e); s != "" && e != nil {
					vals = append(vals, s)
				}
			}
		case string:
			if x != "" {
				vals = []string{x}
			}
		}
		if len(vals) == 0 {
			return ""
		}
		if f.Type == Bool {
			// 布尔列以 "1"/"0" 枚举暴露，转真布尔
			bools := map[bool]struct{}{}
			for _, v := range vals {
				lv := strings.ToLower(strings.TrimSpace(v))
				bools[lv == "1" || lv == "true" || lv == "yes" || lv == "y" || lv == "t"] = struct{}{}
			}
			var bs []any
			for b := range bools {
				bs = append(bs, b)
			}
			q := anyCol(f.Cols, "%s = ANY(%s)", args.Add(bs))
			if op == "nin" {
				return "NOT " + q
			}
			return q
		}
		q := anyCol(f.Cols, "%s = ANY(%s)", args.Add(vals))
		if op == "nin" {
			return "NOT " + q
		}
		return q

	case Number:
		col := f.Cols[0]
		if op == "between" {
			lo, hi := pair(value)
			var parts []string
			if n, ok := toFloat(lo); ok {
				parts = append(parts, fmt.Sprintf("%s >= %s", col, args.Add(n)))
			}
			if n, ok := toFloat(hi); ok {
				parts = append(parts, fmt.Sprintf("%s <= %s", col, args.Add(n)))
			}
			if len(parts) == 0 {
				return ""
			}
			return "(" + strings.Join(parts, " AND ") + ")"
		}
		n, ok := toFloat(value)
		if !ok {
			return ""
		}
		sym := map[string]string{"gt": ">", "lt": "<", "gte": ">=", "lte": "<=", "eq": "="}[op]
		if sym == "" {
			return ""
		}
		return fmt.Sprintf("%s %s %s", col, sym, args.Add(n))

	case Date:
		col := localDate(f.Cols[0])
		if op == "between" {
			lo, hi := pair(value)
			var parts []string
			if s := fmt.Sprint(lo); s != "" {
				parts = append(parts, fmt.Sprintf("%s >= %s::date", col, args.Add(s)))
			}
			if s := fmt.Sprint(hi); s != "" {
				parts = append(parts, fmt.Sprintf("%s <= %s::date", col, args.Add(s)))
			}
			if len(parts) == 0 {
				return ""
			}
			return "(" + strings.Join(parts, " AND ") + ")"
		}
		s, _ := value.(string)
		if s == "" {
			return ""
		}
		sym := map[string]string{"on": "=", "after": ">=", "before": "<="}[op]
		if sym == "" {
			return ""
		}
		return fmt.Sprintf("%s %s %s::date", col, sym, args.Add(s))
	}
	return ""
}

// Apply 解析 filter=<JSON> 并返回 SQL 片段。
//
// 未知字段/未知运算符按 Django 侧行为**静默忽略**（宽容解析）；
// 但**已知字段上的非法值**要报出来——那不是"这个条件不认识"，
// 而是"这个条件填错了"，忽略它等于把没筛过的全量当成筛过的给出去。
func Apply(raw string, fields map[string]FilterField, args *Args) (string, error) {
	if raw == "" || len(fields) == 0 {
		return "", nil
	}
	var m model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", nil
	}
	joiner := " AND "
	if strings.EqualFold(m.Combinator, "or") {
		joiner = " OR "
	}
	var parts []string
	for _, c := range m.Conditions {
		f, ok := fields[c.Field]
		if !ok {
			continue
		}
		if err := checkValues(f, c.Field, c.Op, c.Value); err != nil {
			return "", err
		}
		if sql := condSQL(f, c.Op, c.Value, args); sql != "" {
			parts = append(parts, sql)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, joiner) + ")", nil
}
