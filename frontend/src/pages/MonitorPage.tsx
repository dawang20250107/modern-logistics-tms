import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet } from "../api/client";
import { toast } from "../api/toast";
import type { VehicleState } from "../api/types";
import { useEventStream } from "../api/useEventStream";
import { LiveMap } from "../components/LiveMap";

export function MonitorPage() {
  const queryClient = useQueryClient();
  // 报警流从空开始，仅渲染真实 SSE 推送的告警（此前会预置演示假报警，误导为真实事件）
  const [liveAlerts, setLiveAlerts] = useState<Array<{ message: string; level: string; t: number; type?: string; aiSuggest?: string; actionLabel?: string }>>([]);

  const live = useQuery({
    queryKey: ["telematics", "live"],
    queryFn: () => apiGet<{ vehicles: VehicleState[] }>("/telematics/vehicles/live"),
    refetchInterval: 15000,
  });

  useEventStream((e) => {
    if (e.type === "alert") {
      const msg = String(e.data.message ?? "");
      let type = "general";
      let aiSuggest = "设备报警，请跟进并向承运商核实。";
      let actionLabel = "联系车队";

      if (msg.includes("温度")) {
        type = "temperature";
        aiSuggest = "冷箱温度波动，请联系司机复核冷机状态。";
        actionLabel = "联系司机";
      } else if (msg.includes("超速")) {
        type = "speed";
        aiSuggest = "车辆超速，请下发限速提醒。";
        actionLabel = "联系司机";
      } else if (msg.includes("油") || msg.includes("燃油")) {
        type = "fuel";
        aiSuggest = "疑似漏油或盗油，请联系就近服务站排查。";
        actionLabel = "安排排查";
      }

      setLiveAlerts((prev) =>
        [
          { message: msg, level: String(e.data.level ?? "medium"), t: e.t, type, aiSuggest, actionLabel },
          ...prev
        ].slice(0, 30),
      );
      queryClient.invalidateQueries({ queryKey: ["telematics", "live"] });
    }
  });

  const vehicles = live.data?.vehicles ?? [];
  const online = vehicles.filter((v) => v.online);
  const offline = vehicles.filter((v) => !v.online);

  const handleActionClick = (label: string) => {
    // 微信/电话外呼为预留集成（参见 apps.integrations.wechat，需配置企业微信凭证后接入），
    // 此前的版本会谎称"已外呼/已发送"，现改为如实告知通道未启用，避免调度误以为已联系到司机。
    toast.info(`「${label}」对应的外呼/微信通道尚未配置（预留集成），请改用电话或企业微信直接联系。`);
  };

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
        
        {/* === 主动安全事件流（实时车联网事件） === */}
        <div className="panel" style={{ display: "flex", flexDirection: "column" }}>
          <div className="panel-head" style={{ borderBottom: "1px solid var(--line)", paddingBottom: 10 }}>
            实时报警
          </div>
          <div style={{ flex: 1, padding: "12px 14px", overflowY: "auto", display: "flex", flexDirection: "column", gap: 12, maxHeight: 380 }}>
            {liveAlerts.length === 0 ? (
              <div className="muted small" style={{ textAlign: "center", padding: 20 }}>暂无实时报警</div>
            ) : (
              liveAlerts.map((a, i) => {
                const colors: Record<string, string> = {
                  temperature: "rgba(41,128,185,0.06)",
                  route: "rgba(155,89,182,0.06)",
                  fuel: "rgba(230,126,34,0.06)",
                  speed: "rgba(241,196,15,0.06)"
                };
                const borderColors: Record<string, string> = {
                  temperature: "#2980b9",
                  route: "#9b59b6",
                  fuel: "#e67e22",
                  speed: "#f1c40f"
                };
                
                return (
                  <div 
                    key={i} 
                    style={{ 
                      padding: 12, borderRadius: 8, borderLeft: `4px solid ${borderColors[a.type || "general"] || "var(--primary)"}`,
                      background: colors[a.type || "general"] || "rgba(0,0,0,0.01)", borderTop: "1px solid var(--line)",
                      borderRight: "1px solid var(--line)", borderBottom: "1px solid var(--line)",
                      display: "flex", flexDirection: "column", gap: 6, transition: "all 0.15s ease"
                    }}
                  >
                    {/* 头部：报警类型、级别和时间 */}
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <span style={{ fontSize: 13, fontWeight: "bold" }}>
                        {a.type === "temperature" ? "冷链温度异常" : a.type === "route" ? "车辆偏航预警" : a.type === "fuel" ? "油量异常" : "设备报警"}
                      </span>
                      <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
                        <span className={`tag tag-${a.level === "high" ? "high" : "medium"}`} style={{ fontSize: 10 }}>{a.level === "high" ? "极高危" : "中危"}</span>
                        <span className="muted small" style={{ fontSize: 10 }}>{new Date(a.t).toLocaleTimeString()}</span>
                      </div>
                    </div>

                    {/* 正文：车联网事件消息 */}
                    <p style={{ fontSize: 12, lineHeight: 1.4, margin: 0, color: "var(--text)" }}>{a.message}</p>

                    {/* AI 运营建议（拼单降本 / 风险核查） */}
                    {a.aiSuggest && (
                      <div style={{ padding: "6px 10px", background: "rgba(0,0,0,0.02)", borderRadius: 6, fontSize: 11, lineHeight: 1.5, color: "var(--text-soft)", border: "1px dashed var(--line-strong)" }}>
                        {a.aiSuggest}
                      </div>
                    )}

                    {/* 动作行动面板 */}
                    {a.actionLabel && (
                      <div style={{ display: "flex", gap: 6, marginTop: 4 }}>
                        <button className="btn-primary" style={{ padding: "4px 10px", fontSize: 11 }} onClick={() => handleActionClick(a.actionLabel || "处置")}>
                          {a.actionLabel}
                        </button>
                        <button className="btn-ghost" style={{ padding: "4px 10px", fontSize: 11 }} onClick={() => handleActionClick("微信群安全提醒")}>
                          发送提醒
                        </button>
                      </div>
                    )}
                  </div>
                );
              })
            )}
          </div>
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
