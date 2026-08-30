package main

// 往查询参数里塞 SQL，服务端不能被带跑偏。
//
// 这一块代码上是干净的：值一律走 `$N` 占位符（filters.Args.Add），
// 列名一律来自静态白名单（OrderingCols / SearchCols / DirectParams / WriteCfg.Fields），
// 未知的排序键直接跳过而不是原样拼进 ORDER BY。
//
// 但"现在是干净的"和"以后也是"不是一回事：**最容易破的是排序**——
// 想支持一个新字段时，`col, ok := cfg.OrderingCols[key]; if !ok { continue }`
// 改成 `col := key` 只有一行，读起来还挺自然，而那一行就是注入。
//
// 所以从外面按：塞进去的东西必须被当成数据或被忽略，
// 不能让服务 500（500 说明它进到 SQL 里了），也不能改变结果集的语义。

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (e *testEnv) orderTotal(token, query string) (int, int) {
	e.t.Helper()
	rec := e.call(token, "GET", "/api/v1/orders?page_size=1&"+query, "")
	if rec.Code != http.StatusOK {
		return rec.Code, -1
	}
	var out struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析失败：%v", err)
	}
	return rec.Code, out.Data.Total
}

func TestQueryParamsCannotInjectSQL(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)

	_, base := e.orderTotal(token, "")
	if base <= 0 {
		t.Skip("库里没有订单，这条用例测不到东西")
	}

	// 排序：未知键要被忽略，结果集不变
	for _, payload := range []string{
		"id;DROP TABLE ops_order",
		"(SELECT 1)",
		"o.id/**/UNION/**/SELECT/**/1",
		"created_at, (SELECT count(*) FROM accounts_user)",
		"-'",
	} {
		code, total := e.orderTotal(token, "ordering="+url.QueryEscape(payload))
		if code != http.StatusOK {
			t.Errorf("ordering=%q 返回 %d —— 说明它被拼进 SQL 了", payload, code)
			continue
		}
		if total != base {
			t.Errorf("ordering=%q 让总数从 %d 变成 %d —— 排序参数不该改变结果集", payload, base, total)
		}
	}

	// 搜索：引号、百分号、下划线都是普通字符
	for _, c := range []struct{ payload, why string }{
		{"'", "单引号——最经典的那一个"},
		{"' OR '1'='1", "永真式：如果被拼进去，总数会变成全库"},
		{"%", "ILIKE 的通配符，作为搜索词应当被当成字面量或至少不报错"},
		{"\\", "反斜杠转义"},
		{"a'; SELECT pg_sleep(5); --", "堆叠查询"},
	} {
		code, total := e.orderTotal(token, "search="+url.QueryEscape(c.payload))
		if code != http.StatusOK {
			t.Errorf("search=%q 返回 %d（%s）—— 说明它进到 SQL 里了", c.payload, code, c.why)
			continue
		}
		if total > base {
			t.Errorf("search=%q 搜出 %d 条，比不搜（%d）还多（%s）", c.payload, total, base, c.why)
		}
	}

	// 筛选分两档，期望不同：
	//   未知**字段名/运算符** → 静默忽略（宽容解析，和 Django 侧一致）
	//   已知字段上的**非法值** → 400 并说清是哪个字段
	//     （从前是 500：值作为 $N 传给 ::date，参数化挡住了注入，
	//      但挡不住"这压根不是个日期"，Postgres 直接报错）
	for _, cond := range []map[string]any{
		{"field": "o.id) OR (1=1", "op": "eq", "value": "x"},
		{"field": "order_no", "op": "eq); DROP TABLE ops_order; --", "value": "x"},
	} {
		q, _ := json.Marshal(map[string]any{
			"combinator": "and",
			"conditions": []map[string]any{cond},
		})
		code, _ := e.orderTotal(token, "filter="+url.QueryEscape(string(q)))
		if code != http.StatusOK {
			t.Errorf("filter=%s 返回 %d —— 未知字段/运算符应被静默忽略，不该报错", q, code)
		}
	}
	for _, bad := range []string{
		"2026-01-01'; SELECT 1; --",
		"今天",         // 用户在日期框里随手打的字
		"2026-13-45", // 打错的日期
	} {
		q, _ := json.Marshal(map[string]any{
			"combinator": "and",
			"conditions": []map[string]any{{"field": "created_at", "op": "on", "value": bad}},
		})
		rec := e.call(token, "GET", "/api/v1/orders?page_size=1&filter="+url.QueryEscape(string(q)), "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("日期值 %q 返回 %d，应该是 400。\n"+
				"  500 的含义是「服务端出故障了」——它会进错误率、会触发告警、"+
				"会让人去查网关和数据库，而这里只是有人填了个不是日期的东西。", bad, rec.Code)
			continue
		}
		if body := rec.Body.String(); !strings.Contains(body, "created_at") {
			t.Errorf("日期值 %q 的报错没说是哪个字段：%s", bad, body)
		}
	}
	// 合法的两种写法都要照常能用
	for _, good := range []string{"2026-01-01", "2026/01/01"} {
		q, _ := json.Marshal(map[string]any{
			"combinator": "and",
			"conditions": []map[string]any{{"field": "created_at", "op": "on", "value": good}},
		})
		if code, _ := e.orderTotal(token, "filter="+url.QueryEscape(string(q))); code != http.StatusOK {
			t.Errorf("合法日期 %q 被拒了（%d）—— 校验收得太紧", good, code)
		}
	}

	// 收尾：表还在。前面那些 payload 里有 DROP TABLE，
	// 真被执行了的话这一句会失败——这是这条用例最后的兜底。
	if _, after := e.orderTotal(token, ""); after != base {
		t.Fatalf("跑完这些 payload 之后订单总数从 %d 变成 %d —— 有东西真的被执行了", base, after)
	}
}
