import { useEffect, useRef } from "react";

import { AMAP_KEY, useAmap } from "./useAmap";

export interface Trajectory {
  waybill_no: string;
  points: Array<{ lng: number; lat: number; speed_kmh: number; reported_at: string }>;
  stops: Array<{ lng: number; lat: number; duration_seconds: number; from: string; to: string }>;
  overspeed_segments: Array<{ from: string; to: string; max_speed: number }>;
  total_points: number;
}

/* eslint-disable @typescript-eslint/no-explicit-any */
export function TrajectoryMap({ traj, height = 360 }: { traj: Trajectory; height?: number }) {
  const amap = useAmap();
  const ref = useRef<HTMLDivElement>(null);
  const mapRef = useRef<any>(null);

  useEffect(() => {
    if (!amap || !ref.current || traj.points.length === 0) return;
    const AMap = amap as any;
    if (!mapRef.current) {
      mapRef.current = new AMap.Map(ref.current, { zoom: 6, resizeEnable: true });
    }
    const map = mapRef.current;
    map.clearMap();
    const path = traj.points.filter((p) => p.lng && p.lat).map((p) => [p.lng, p.lat]);
    if (path.length === 0) return;
    const line = new AMap.Polyline({
      path, strokeColor: "#2f6bff", strokeWeight: 5, strokeOpacity: 0.9, lineJoin: "round", showDir: true,
    });
    map.add(line);
    // 起终点
    map.add(new AMap.Marker({ position: path[0], title: "起点" }));
    map.add(new AMap.Marker({ position: path[path.length - 1], title: "终点" }));
    // 停留点
    traj.stops.forEach((s) => {
      const c = new AMap.Circle({
        center: [s.lng, s.lat], radius: 600, strokeColor: "#ef9d10", fillColor: "#ef9d10", fillOpacity: 0.25, strokeWeight: 1,
      });
      map.add(c);
    });
    map.setFitView();
  }, [amap, traj]);

  const stats = (
    <div className="kv" style={{ padding: "12px 16px 0" }}>
      <div><span>轨迹点</span><b>{traj.total_points}</b></div>
      <div><span>停留点</span><b>{traj.stops.length}</b></div>
      <div><span>超速段</span><b style={traj.overspeed_segments.length ? { color: "var(--red)" } : {}}>{traj.overspeed_segments.length}</b></div>
    </div>
  );

  if (!AMAP_KEY) {
    return (
      <div>
        {stats}
        <div className="muted small" style={{ padding: 16, textAlign: "center" }}>
          未配置高德地图 Key（VITE_AMAP_KEY），仅显示轨迹统计。
        </div>
      </div>
    );
  }
  if (traj.points.length === 0) {
    return <div className="muted small" style={{ padding: 16 }}>暂无轨迹数据。</div>;
  }
  return (
    <div>
      {stats}
      <div ref={ref} style={{ width: "100%", height, borderRadius: 8, marginTop: 12 }} />
    </div>
  );
}
