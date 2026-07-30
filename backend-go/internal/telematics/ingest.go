package telematics

// 设备上报与轨迹上报的削峰落库。
//
// Django 侧是「请求压 Redis 队列 → Celery 任务批量落库」，为的是让高并发写热点
// 不直打主库。Go 侧同样不在请求里落库，改用进程内有界队列 + 后台批处理协程：
// 少一整套 Redis + Celery 依赖，语义（202 + 异步持久化）与吞吐特征都保持一致。
//
// 有界是关键：队列满时直接丢弃并计数，宁可丢采样点也不让内存无限膨胀把网关拖垮
// ——轨迹点是可容忍稀疏的时序采样，网关不可用则是全站故障。

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	telemetryQueueSize = 20000
	trackingQueueSize  = 50000
	flushBatch         = 500
	flushInterval      = 2 * time.Second
)

// TrackPointReport 轨迹上报（/tracking/points）
type TrackPointReport struct {
	WaybillNo  string   `json:"waybill_no"`
	Lng        *float64 `json:"lng"`
	Lat        *float64 `json:"lat"`
	SpeedKmh   *float64 `json:"speed_kmh"`
	ReportedAt string   `json:"reported_at"`
	Provider   string   `json:"provider"`
}

// Ingestor 上报管道：两条独立队列 + 各自的批处理协程
type Ingestor struct {
	db        *pgxpool.Pool
	telemetry chan Report
	tracking  chan TrackPointReport
	dropped   atomic.Int64
}

func NewIngestor(db *pgxpool.Pool) *Ingestor {
	return &Ingestor{
		db:        db,
		telemetry: make(chan Report, telemetryQueueSize),
		tracking:  make(chan TrackPointReport, trackingQueueSize),
	}
}

// Start 拉起后台批处理协程（随进程生命周期）
func (in *Ingestor) Start(ctx context.Context) {
	go in.loopTelemetry(ctx)
	go in.loopTracking(ctx)
}

// enqueueTelemetry 非阻塞入队；队列满则丢弃并计数
func (in *Ingestor) enqueueTelemetry(r Report) bool {
	select {
	case in.telemetry <- r:
		return true
	default:
		in.dropped.Add(1)
		return false
	}
}

func (in *Ingestor) enqueueTracking(p TrackPointReport) bool {
	select {
	case in.tracking <- p:
		return true
	default:
		in.dropped.Add(1)
		return false
	}
}

func (in *Ingestor) loopTelemetry(ctx context.Context) {
	tick := time.NewTicker(flushInterval)
	defer tick.Stop()
	buf := make([]Report, 0, flushBatch)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		in.persistReports(ctx, buf)
		buf = buf[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case r := <-in.telemetry:
			buf = append(buf, r)
			if len(buf) >= flushBatch {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func (in *Ingestor) loopTracking(ctx context.Context) {
	tick := time.NewTicker(flushInterval)
	defer tick.Stop()
	buf := make([]TrackPointReport, 0, flushBatch)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		in.persistTrackingPoints(ctx, buf)
		buf = buf[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case p := <-in.tracking:
			buf = append(buf, p)
			if len(buf) >= flushBatch {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

// parseReportedAt 对齐 parse_datetime(...) or timezone.now()
func parseReportedAt(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}

// persistReports 批量落库：设备心跳 + 车辆实时状态 + 轨迹续点 + 规则报警，
// 对齐 services.persist_reports。
func (in *Ingestor) persistReports(ctx context.Context, reports []Report) {
	fences := loadGeofences(ctx, in.db)
	var points []TrackPointReport
	for _, r := range reports {
		reportedAt := parseReportedAt(r.ReportedAt)

		var deviceID, deviceVehicleID *string
		if r.DeviceNo != "" {
			var id string
			var vid *string
			if in.db.QueryRow(ctx, "SELECT id::text, vehicle_id::text FROM tel_device WHERE device_no=$1",
				r.DeviceNo).Scan(&id, &vid) == nil {
				deviceID, deviceVehicleID = &id, vid
				_, _ = in.db.Exec(ctx, `
					UPDATE tel_device SET last_seen_at=$2, status='online', updated_at=now() WHERE id=$1::uuid`,
					id, reportedAt)
			}
		}
		var waybillID *string
		if r.WaybillNo != "" {
			var id string
			if in.db.QueryRow(ctx, "SELECT id::text FROM ops_waybill WHERE waybill_no=$1", r.WaybillNo).
				Scan(&id) == nil {
				waybillID = &id
			}
		}
		vehicleID := deviceVehicleID
		if r.VehiclePlate != "" {
			var id string
			if in.db.QueryRow(ctx, "SELECT id::text FROM md_vehicle WHERE plate_no=$1 AND NOT is_deleted",
				r.VehiclePlate).Scan(&id) == nil {
				vehicleID = &id
			}
		}

		if vehicleID != nil {
			in.upsertVehicleState(ctx, *vehicleID, waybillID, r, reportedAt)
		}
		if waybillID != nil && r.Lng != nil && r.Lat != nil {
			points = append(points, TrackPointReport{
				WaybillNo: r.WaybillNo, Lng: r.Lng, Lat: r.Lat,
				SpeedKmh: r.SpeedKmh, ReportedAt: r.ReportedAt, Provider: r.Provider,
			})
		}
		for _, spec := range evaluateTelemetry(r) {
			raiseAlert(ctx, in.db, spec, vehicleID, deviceID, waybillID, reportedAt, true)
		}
		if vehicleID != nil && r.Lng != nil && r.Lat != nil {
			evaluateGeofences(ctx, in.db, vehicleID, *r.Lng, *r.Lat, waybillID, reportedAt, fences)
			evaluateDeviation(ctx, in.db, vehicleID, *r.Lng, *r.Lat, waybillID, reportedAt)
		}
	}
	if len(points) > 0 {
		in.persistTrackingPoints(ctx, points)
	}
}

func (in *Ingestor) upsertVehicleState(ctx context.Context, vehicleID string, waybillID *string, r Report, at time.Time) {
	id, _ := uuid.NewV7()
	_, err := in.db.Exec(ctx, `
		INSERT INTO tel_vehicle_state (id, created_at, updated_at, vehicle_id, waybill_id, lng, lat,
		  speed_kmh, heading, mileage_km, temperature_c, fuel_pct, online, reported_at)
		VALUES ($1, now(), now(), $2::uuid, $3::uuid, $4::numeric, $5::numeric, $6::numeric,
		        $7, $8::numeric, $9::numeric, $10::numeric, true, $11)
		ON CONFLICT (vehicle_id) DO UPDATE SET
		  waybill_id=EXCLUDED.waybill_id, lng=EXCLUDED.lng, lat=EXCLUDED.lat,
		  speed_kmh=EXCLUDED.speed_kmh, heading=EXCLUDED.heading, mileage_km=EXCLUDED.mileage_km,
		  temperature_c=EXCLUDED.temperature_c, fuel_pct=EXCLUDED.fuel_pct,
		  online=true, reported_at=EXCLUDED.reported_at, updated_at=now()`,
		id.String(), vehicleID, waybillID, orZeroF(r.Lng), orZeroF(r.Lat), orZeroF(r.SpeedKmh),
		int(orZeroF(r.Heading)), orZeroF(r.MileageKm), r.TemperatureC, r.FuelPct, at)
	if err != nil {
		slog.Warn("vehicle state upsert", "err", err)
	}
}

func orZeroF(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// persistTrackingPoints 轨迹点批量落库（COPY 走 pgx 的 CopyFrom）
func (in *Ingestor) persistTrackingPoints(ctx context.Context, points []TrackPointReport) {
	// 先把业务单号批量翻成主键，避免逐点查
	ids := map[string]string{}
	for _, p := range points {
		ids[p.WaybillNo] = ""
	}
	nos := make([]string, 0, len(ids))
	for no := range ids {
		nos = append(nos, no)
	}
	rows, err := in.db.Query(ctx, "SELECT waybill_no, id::text FROM ops_waybill WHERE waybill_no = ANY($1)", nos)
	if err != nil {
		slog.Warn("tracking waybill lookup", "err", err)
		return
	}
	for rows.Next() {
		var no, id string
		if rows.Scan(&no, &id) == nil {
			ids[no] = id
		}
	}
	rows.Close()

	batch := make([][]any, 0, len(points))
	now := time.Now()
	for _, p := range points {
		id, ok := ids[p.WaybillNo]
		if !ok || id == "" || p.Lng == nil || p.Lat == nil {
			continue
		}
		nid, _ := uuid.NewV7()
		batch = append(batch, []any{
			nid.String(), now, now, *p.Lng, *p.Lat, orZeroF(p.SpeedKmh),
			parseReportedAt(p.ReportedAt), p.Provider, id,
		})
	}
	if len(batch) == 0 {
		return
	}
	if _, err := in.db.CopyFrom(ctx, pgx.Identifier{"ops_tracking_point"},
		[]string{"id", "created_at", "updated_at", "lng", "lat", "speed_kmh", "reported_at", "provider", "waybill_id"},
		pgx.CopyFromRows(batch)); err != nil {
		slog.Warn("tracking points copy", "err", err)
	}
}

// ScanOfflineDevices 周期扫描超时未上报的设备/车辆并置离线 + 报警，
// 对齐 services.scan_offline_devices（Django 由 celery beat 驱动）。
func (in *Ingestor) ScanOfflineDevices(ctx context.Context) int {
	rows, err := in.db.Query(ctx, `
		SELECT id::text, device_no, vehicle_id::text FROM tel_device
		WHERE status='online' AND (last_seen_at IS NULL OR last_seen_at < now() - make_interval(mins => $1))`,
		offlineMinutes)
	if err != nil {
		slog.Warn("offline scan", "err", err)
		return 0
	}
	type dev struct {
		id, no  string
		vehicle *string
	}
	var devs []dev
	for rows.Next() {
		var d dev
		if rows.Scan(&d.id, &d.no, &d.vehicle) == nil {
			devs = append(devs, d)
		}
	}
	rows.Close()
	for _, d := range devs {
		_, _ = in.db.Exec(ctx, "UPDATE tel_device SET status='offline', updated_at=now() WHERE id=$1::uuid", d.id)
		if d.vehicle != nil {
			_, _ = in.db.Exec(ctx,
				"UPDATE tel_vehicle_state SET online=false, updated_at=now() WHERE vehicle_id=$1::uuid", *d.vehicle)
		}
		raiseAlert(ctx, in.db, alertSpec{
			AlertType: "offline", Level: "medium",
			Message: fmt.Sprintf("设备 %s 超过 %d 分钟未上报", d.no, offlineMinutes),
		}, d.vehicle, &d.id, nil, time.Now(), true)
	}
	return len(devs)
}

// StartOfflineScanner 周期兜底扫描（替代 celery beat）
func (in *Ingestor) StartOfflineScanner(ctx context.Context, every time.Duration) {
	go func() {
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				in.ScanOfflineDevices(ctx)
			}
		}
	}()
}
