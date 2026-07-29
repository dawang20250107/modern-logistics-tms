package telematics

// 地理计算与轨迹分析（纯函数），逐行对齐 apps/telematics/geo.py。
// 停留点与超速段的判定口径直接决定「异常停车」「超速」这类报警是否成立，
// 属于要被拿去和司机、承运商对账的结论，因此按原算法复刻而非另起炉灶。

import (
	"math"
	"time"
)

const earthRadiusM = 6_371_000.0

// HaversineM 两点球面距离（米）
func HaversineM(lng1, lat1, lng2, lat2 float64) float64 {
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dphi := (lat2 - lat1) * math.Pi / 180
	dlmb := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dphi/2)*math.Sin(dphi/2) +
		math.Cos(p1)*math.Cos(p2)*math.Sin(dlmb/2)*math.Sin(dlmb/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(a))
}

func pointInCircle(lng, lat, cLng, cLat, radiusM float64) bool {
	return HaversineM(lng, lat, cLng, cLat) <= radiusM
}

// pointInPolygon 射线法；polygon 为 [[lng,lat], ...]
func pointInPolygon(lng, lat float64, polygon [][]float64) bool {
	if len(polygon) < 3 {
		return false
	}
	inside := false
	n := len(polygon)
	j := n - 1
	for i := 0; i < n; i++ {
		if len(polygon[i]) < 2 || len(polygon[j]) < 2 {
			j = i
			continue
		}
		xi, yi := polygon[i][0], polygon[i][1]
		xj, yj := polygon[j][0], polygon[j][1]
		den := yj - yi
		if den == 0 {
			den = 1e-12 // 对齐 Python 的 (yj - yi) or 1e-12
		}
		if (yi > lat) != (yj > lat) && lng < (xj-xi)*(lat-yi)/den+xi {
			inside = !inside
		}
		j = i
	}
	return inside
}

// distanceToPolylineM 点到折线（规划路线）的最短距离（米）
func distanceToPolylineM(lng, lat float64, line [][]float64) float64 {
	if len(line) == 0 {
		return math.Inf(1)
	}
	if len(line) == 1 {
		return HaversineM(lng, lat, line[0][0], line[0][1])
	}
	best := math.Inf(1)
	for i := 0; i+1 < len(line); i++ {
		a, b := line[i], line[i+1]
		if len(a) < 2 || len(b) < 2 {
			continue
		}
		if d := pointSegmentDistM(lng, lat, a[0], a[1], b[0], b[1]); d < best {
			best = d
		}
	}
	return best
}

// pointSegmentDistM 点到线段距离（米），等距投影近似（短距离足够精确）
func pointSegmentDistM(plng, plat, alng, alat, blng, blat float64) float64 {
	lat0 := (alat + blat) / 2 * math.Pi / 180
	mx := math.Cos(lat0) * earthRadiusM * math.Pi / 180
	my := earthRadiusM * math.Pi / 180
	px, py := plng*mx, plat*my
	ax, ay := alng*mx, alat*my
	bx, by := blng*mx, blat*my
	dx, dy := bx-ax, by-ay
	seg2 := dx*dx + dy*dy
	if seg2 == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / seg2
	t = math.Max(0, math.Min(1, t))
	cx, cy := ax+t*dx, ay+t*dy
	return math.Hypot(px-cx, py-cy)
}

// TrackPoint 轨迹点（已按时间升序）
type TrackPoint struct {
	Lng, Lat, SpeedKmh float64
	ReportedAt         time.Time
}

type Stop struct {
	Lng, Lat        float64
	From, To        time.Time
	DurationSeconds int
}

type OverspeedSegment struct {
	From, To time.Time
	MaxSpeed float64
}

type TrajectoryAnalysis struct {
	Stops             []Stop
	OverspeedSegments []OverspeedSegment
	TotalPoints       int
}

// AnalyzeTrajectory 停留点 + 超速段，对齐 geo.analyze_trajectory
// （停留：半径 200m 内聚簇且持续 ≥600s；超速：连续超过限速的区段）
func AnalyzeTrajectory(points []TrackPoint, stopRadiusM float64, stopSeconds int, speedLimit float64) TrajectoryAnalysis {
	out := TrajectoryAnalysis{Stops: []Stop{}, OverspeedSegments: []OverspeedSegment{}, TotalPoints: len(points)}
	n := len(points)
	for i := 0; i < n; {
		j := i + 1
		for j < n && HaversineM(points[i].Lng, points[i].Lat, points[j].Lng, points[j].Lat) <= stopRadiusM {
			j++
		}
		duration := points[j-1].ReportedAt.Sub(points[i].ReportedAt).Seconds()
		if j-i >= 2 && duration >= float64(stopSeconds) {
			out.Stops = append(out.Stops, Stop{
				Lng: points[i].Lng, Lat: points[i].Lat,
				From: points[i].ReportedAt, To: points[j-1].ReportedAt,
				DurationSeconds: int(duration),
			})
			i = j
		} else {
			i++
		}
	}

	var seg *OverspeedSegment
	for _, p := range points {
		if p.SpeedKmh > speedLimit {
			if seg == nil {
				seg = &OverspeedSegment{From: p.ReportedAt, To: p.ReportedAt, MaxSpeed: p.SpeedKmh}
			} else {
				seg.To = p.ReportedAt
				seg.MaxSpeed = math.Max(seg.MaxSpeed, p.SpeedKmh)
			}
		} else if seg != nil {
			out.OverspeedSegments = append(out.OverspeedSegments, *seg)
			seg = nil
		}
	}
	if seg != nil {
		out.OverspeedSegments = append(out.OverspeedSegments, *seg)
	}
	return out
}
