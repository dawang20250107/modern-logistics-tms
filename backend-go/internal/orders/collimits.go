package orders

// 建单前按库里真实的列长校验。
//
// 由来：给「客服」角色补上建单权限之后，第一次真的把这颗按钮按下去，
// 拿到的是 **HTTP 500 + 一句原始 Postgres 报错**：
//
//	建单失败：ERROR: value too long for type character varying(32) (SQLSTATE 22001)
//
// 七个字段试下来全是这一句：联系电话、包装、温区、提货联系电话、
// 货物名称、始发地、客户名称。共同点是**它不说是哪个字段**——
// 客服粘一段长地址进来，得到的是一句他看不懂、也无从下手的英文，
// 只能去问技术。而这是下单，是整条链的第一步。
//
// 硬编码一份"字段 → 最大长度"会和迁移漂开（列改宽了，校验还卡在旧值上，
// 而且是那种没人会发现的松/紧）。所以直接问库：information_schema
// 里就有真值，进程内缓存一次。列改了，校验跟着改。

import (
	"context"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type limitCache struct {
	once sync.Once
	m    map[string]map[string]int // table -> column -> 最大字符数
	err  error
}

var colLimits limitCache

// load 一次性读出这几张表里所有定长字符列的上限。
func (c *limitCache) load(ctx context.Context, db *pgxpool.Pool, tables []string) map[string]map[string]int {
	c.once.Do(func() {
		m := map[string]map[string]int{}
		rows, err := db.Query(ctx, `
			SELECT table_name, column_name, character_maximum_length
			FROM information_schema.columns
			WHERE table_schema='public' AND table_name = ANY($1)
			  AND character_maximum_length IS NOT NULL`, tables)
		if err != nil {
			c.err = err
			return
		}
		defer rows.Close()
		for rows.Next() {
			var t, col string
			var n int
			if err := rows.Scan(&t, &col, &n); err != nil {
				c.err = err
				return
			}
			if m[t] == nil {
				m[t] = map[string]int{}
			}
			m[t][col] = n
		}
		c.m = m
	})
	return c.m
}

// tooLong 检查一组「列名 → 值」，返回第一个超长的列名和它的上限。
//
// 按**字符**数比，不是字节：Postgres 的 varchar(32) 数的是字符，
// 一个汉字算一个。按字节比会把 10 个汉字的地址误判成超长。
func tooLong(limits map[string]int, cols []string, vals []any) (string, int, int) {
	if limits == nil {
		return "", 0, 0
	}
	for i, c := range cols {
		max, ok := limits[c]
		if !ok || i >= len(vals) {
			continue
		}
		s, isStr := vals[i].(string)
		if !isStr {
			continue
		}
		if n := len([]rune(s)); n > max {
			return c, max, n
		}
	}
	return "", 0, 0
}

// tooLongMsg 拼一句客服看得懂、能照着改的话。
func tooLongMsg(col string, max, got int) string {
	label := fieldLabels[col]
	if label == "" {
		label = col
	}
	return label + "太长了：最多 " + itoa(max) + " 个字，现在有 " + itoa(got) + " 个。"
}

// clipRunes 按字符截断——用于系统自己拼出来的值（比如来源标识"组织·姓名"）。
// 那种值不该因为组织名长就把下单挡回去，截断即可。
func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max]))
}
