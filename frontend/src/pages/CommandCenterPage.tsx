import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import type { Paginated, VehicleState, Waybill } from "../api/types";
import { useEventStream } from "../api/useEventStream";
import { LiveMap } from "../components/LiveMap";
import { StateView } from "../components/StateView";

interface Summary {
  online_vehicles: number;
  offline_vehicles: number;
  pending_dispatch: number;
  in_transit: number;
  open_alerts: number;
  high_alerts: number;
}

interface DispatchRec {
  waybill_no: string;
  best_vehicle: { plate_no: string; utilization: number } | null;
  best_carrier: { carrier: string; quote: number } | null;
}

function Kpi({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <div className={`kpi${tone ? ` kpi-${tone}` : ""}`}>
      <div className="kpi-value">{value}</div>
      <div className="kpi-label">{label}</div>
    </div>
  );
}

export function CommandCenterPage() {
  const queryClient = useQueryClient();
  const [recs, setRecs] = useState<Record<string, DispatchRec>>({});
  const [liveAlerts, setLiveAlerts] = useState<Array<{ message: string; level: string; t: number }>>([]);

  const summary = useQuery({
    queryKey: ["cc", "summary"],
    queryFn: () => apiGet<Summary>("/telematics/command-center/summary"),
    refetchInterval: 15000,
  });
  const live = useQuery({
    queryKey: ["cc", "live"],
    queryFn: () => apiGet<{ vehicles: VehicleState[] }>("/telematics/vehicles/live?online=true"),
    refetchInterval: 15000,
  });
  const pending = useQuery({
    queryKey: ["cc", "pending"],
    queryFn: () => apiGet<Paginated<Waybill>>("/waybills?status=pending_dispatch&page_size=50"),
  });
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["cc"] });
  };

  useEventStream((e) => {
    if (e.type === "alert") {
      setLiveAlerts((prev) =>
        [{ message: String(e.data.message ?? ""), level: String(e.data.level ?? "medium"), t: e.t }, ...prev].slice(0, 25),
      );
    }
    invalidate();
  });

  const recommend = useMutation({
    mutationFn: (no: string) => apiGet<DispatchRec>(`/waybills/${no}/dispatch-recommendation`),
    onSuccess: (data) => setRecs((prev) => ({ ...prev, [data.waybill_no]: data })),
  });
  const planAll = useMutation({
    mutationFn: () => apiPost<{ assigned_count: number; unassigned: string[] }>("/waybills/dispatch-plan", {}),
  });

  const s = summary.data;
  const vehicles = live.data?.vehicles ?? [];
  const pendings = pending.data?.items ?? [];

  return (
    <div className="stack">
      <div className="kpi-row">
        <Kpi label="在线运力" value={s?.online_vehicles ?? 0} tone="blue" />
        <Kpi label="待调度" value={s?.pending_dispatch ?? 0} tone="amber" />
        <Kpi label="在途" value={s?.in_transit ?? 0} />
        <Kpi label="未处理报警" value={s?.open_alerts ?? 0} tone="red" />
      </div>

      <div className="ct-grid">
        <div className="panel">
          <div className="panel-head">指挥地图 · 在线运力 {vehicles.length}</div>
          <LiveMap vehicles={vehicles} height={460} />
        </div>
        <div className="panel">
          <div className="panel-head">实时报警 (SSE)</div>
          {liveAlerts.length === 0 ? (
            <div className="muted small" style={{ padding: 16 }}>暂无报警</div>
          ) : (
            <ul className="event-feed" style={{ maxHeight: 420 }}>
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
        <div className="panel-head">
          待调度池
          <button className="btn-primary" disabled={planAll.isPending} onClick={() => planAll.mutate()}>
            {planAll.isPending ? "排线中…" : "一键排线"}
          </button>
        </div>
        {planAll.data && (
          <div className="ai-answer">
            已分配 {planAll.data.assigned_count} 单
            {planAll.data.unassigned.length > 0 && `，无可用运力 ${planAll.data.unassigned.length} 单`}。
          </div>
        )}
        {pending.isLoading ? (
          <StateView kind="loading" compact />
        ) : pendings.length === 0 ? (
          <StateView kind="empty" scene="pool-empty" />
        ) : (
          <table className="table">
            <thead>
              <tr><th>运单号</th><th>线路</th><th>货量</th><th>调度建议</th><th></th></tr>
            </thead>
            <tbody>
              {pendings.map((w) => {
                const rec = recs[w.waybill_no];
                return (
                  <tr key={w.id}>
                    <td><Link className="link mono" to={`/waybills/${w.waybill_no}`}>{w.waybill_no}</Link></td>
                    <td>{w.origin} → {w.destination}</td>
                    <td>{w.cargo?.weight_ton ?? 0}吨</td>
                    <td className="small">
                      {rec
                        ? rec.best_vehicle
                          ? `${rec.best_vehicle.plate_no}（装载 ${Math.round(rec.best_vehicle.utilization * 100)}%）${rec.best_carrier ? ` · ${rec.best_carrier.carrier} ¥${rec.best_carrier.quote}` : ""}`
                          : "无可用运力"
                        : "—"}
                    </td>
                    <td>
                      <button className="btn-ghost" disabled={recommend.isPending} onClick={() => recommend.mutate(w.waybill_no)}>
                        生成建议
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
