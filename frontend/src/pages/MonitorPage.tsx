import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import type { VehicleState } from "../api/types";
import { useEventStream } from "../api/useEventStream";
import { LiveMap } from "../components/LiveMap";

export function MonitorPage() {
  const queryClient = useQueryClient();
  const [liveAlerts, setLiveAlerts] = useState<Array<{ message: string; level: string; t: number }>>([]);

  const live = useQuery({
    queryKey: ["telematics", "live"],
    queryFn: () => apiGet<{ vehicles: VehicleState[] }>("/telematics/vehicles/live"),
    refetchInterval: 15000,
  });

  useEventStream((e) => {
    if (e.type === "alert") {
      setLiveAlerts((prev) =>
        [{ message: String(e.data.message ?? ""), level: String(e.data.level ?? "medium"), t: e.t }, ...prev].slice(0, 30),
      );
      queryClient.invalidateQueries({ queryKey: ["telematics", "live"] });
    }
  });

  const vehicles = live.data?.vehicles ?? [];
  const online = vehicles.filter((v) => v.online);
  const offline = vehicles.filter((v) => !v.online);

  return (
    <div className="stack">
      <div className="kpi-row">
        <div className="kpi kpi-blue"><div className="kpi-value">{online.length}</div><div className="kpi-label">在线车辆</div></div>
        <div className="kpi"><div className="kpi-value">{offline.length}</div><div className="kpi-label">离线车辆</div></div>
        <div className="kpi kpi-red"><div className="kpi-value">{liveAlerts.length}</div><div className="kpi-label">实时报警</div></div>
      </div>

      <div className="ct-grid">
        <div className="panel">
          <div className="panel-head">实时定位</div>
          <LiveMap vehicles={vehicles} />
        </div>
        <div className="panel">
          <div className="panel-head">实时报警 (SSE)</div>
          {liveAlerts.length === 0 ? (
            <div className="muted small" style={{ padding: 16 }}>已连接，等待报警…</div>
          ) : (
            <ul className="event-feed">
              {liveAlerts.map((a, i) => (
                <li key={`${a.t}-${i}`}>
                  <span className={`tag tag-${a.level === "high" ? "high" : "medium"}`}>{a.level}</span>
                  <span className="small">{a.message}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">车辆列表</div>
        {live.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : vehicles.length === 0 ? (
          <div className="muted" style={{ padding: 16 }}>暂无车辆实时状态</div>
        ) : (
          <table className="table">
            <thead>
              <tr><th>车牌</th><th>状态</th><th>运单</th><th>速度(km/h)</th><th>温度(℃)</th><th>油量(%)</th><th>位置</th><th>更新时间</th></tr>
            </thead>
            <tbody>
              {vehicles.map((v) => (
                <tr key={v.id}>
                  <td className="mono">{v.vehicle_plate}</td>
                  <td><span className={`tag tag-${v.online ? "low" : "high"}`}>{v.online ? "在线" : "离线"}</span></td>
                  <td>{v.waybill_no ? <Link className="link mono" to={`/waybills/${v.waybill_no}`}>{v.waybill_no}</Link> : "-"}</td>
                  <td>{v.speed_kmh}</td>
                  <td>{v.temperature_c ?? "-"}</td>
                  <td>{v.fuel_pct ?? "-"}</td>
                  <td className="mono small">{Number(v.lat).toFixed(4)}, {Number(v.lng).toFixed(4)}</td>
                  <td className="small">{v.reported_at ? new Date(v.reported_at).toLocaleString() : "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
