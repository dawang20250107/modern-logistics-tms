package analytics

// 指标按日物化（替代 Django 的 materialize_metrics 管理命令 + celery beat）。
//
// 趋势端点 /analytics/metrics/{code}/trend 与看板的 trends 段读的都是
// ana_metric_snapshot，而这张表只有读没有写的话，图会随时间一天天变空——
// 不是报错，是静静地少数据，最难发现。故把物化搬进网关的周期协程里。

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// materializeMetrics 需要按日留痕的指标（对齐 apps/analytics/services.MATERIALIZE_METRICS）。
// 只物化「区间型」指标：快照型指标（在途、在线率）问的是"此刻"，昨天的此刻没有意义。
var materializeMetrics = []string{
	"ops.waybill_count",
	"fleet.alert_count",
	"order.count",
	"finance.receivable_total",
	"finance.payable_total",
}

// MaterializeDay 把某一天的指标值落快照，幂等 upsert。返回写入条数。
func MaterializeDay(ctx context.Context, db *pgxpool.Pool, day time.Time) (int, error) {
	d := day.Format("2006-01-02")
	n := 0
	for _, code := range materializeMetrics {
		spec := metricSpecs[code]
		var v float64
		// 单日窗口：起止同为该日，与 compute_metric(start=day, end=day) 等价
		if err := db.QueryRow(ctx, spec.SQL, d, d).Scan(&v); err != nil {
			slog.Error("指标物化取值失败", "code", code, "date", d, "err", err)
			continue
		}
		_, err := db.Exec(ctx, `
			INSERT INTO ana_metric_snapshot (id, created_at, updated_at, metric_code, stat_date, dimension_key, value)
			VALUES ($1, now(), now(), $2, $3::date, '', $4)
			ON CONFLICT (metric_code, stat_date, dimension_key)
			DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
			uuid.Must(uuid.NewV7()), code, d, v)
		if err != nil {
			slog.Error("指标物化写入失败", "code", code, "date", d, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// StartMaterializer 起周期协程物化指标。
//
// 每轮都把「今天」重算一遍（当天数据还在长，昨天定稿的值才是终值），并顺带补齐
// 最近 backfill 天里缺的行——网关停机跨天、或首次部署到一个有历史数据的库时，
// 靠这个把趋势图填上，不必再手工跑一次命令。
func StartMaterializer(ctx context.Context, db *pgxpool.Pool, every time.Duration, backfill int) {
	go func() {
		run := func() {
			today := time.Now().In(cstZone)
			if _, err := MaterializeDay(ctx, db, today); err != nil {
				return
			}
			for i := 1; i <= backfill; i++ {
				day := today.AddDate(0, 0, -i)
				// 按「这天已物化了几个指标」判断，不能只看「这天有没有行」——
				// 后加的指标在历史日期上从来没有过行，会被同日的老指标挡住，永远补不上。
				var have int
				err := db.QueryRow(ctx, `
					SELECT count(DISTINCT metric_code) FROM ana_metric_snapshot
					WHERE stat_date = $1::date AND dimension_key = '' AND metric_code = ANY($2)`,
					day.Format("2006-01-02"), materializeMetrics).Scan(&have)
				if err != nil || have == len(materializeMetrics) {
					continue
				}
				MaterializeDay(ctx, db, day)
			}
		}
		run() // 启动即跑一次，别让新部署的实例空等一个周期
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				run()
			}
		}
	}()
}
