// Package filters 服务端通用筛选：前端 FilterBuilder 的条件模型（filter=<JSON>）→ SQL WHERE 片段。
// 与 Django 侧 apps/core/filtering.py 逐运算符对齐，是各资源列表移植的共用地基。
//
// 模型：{"combinator":"and"|"or","conditions":[{"field","op","value"},...]}
// 每个资源声明 map[key]FilterField{Type, Cols}，Cols 为一个或多个 SQL 列表达式
//（多列用于「线路=起+讫」这类跨列文本，任一列命中即算命中）。
package filters

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

// Apply 解析 filter=<JSON> 并返回 SQL 片段（不含 WHERE 前缀；无有效条件时返回空串）。
// 非法 JSON 与未知字段/运算符按 Django 侧行为静默忽略（宽容解析，绝不 500）。
func Apply(raw string, fields map[string]FilterField, args *Args) string {
	if raw == "" || len(fields) == 0 {
		return ""
	}
	var m model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
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
		if sql := condSQL(f, c.Op, c.Value, args); sql != "" {
			parts = append(parts, sql)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, joiner) + ")"
}
