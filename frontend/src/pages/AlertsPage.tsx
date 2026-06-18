import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import type { Alert, Paginated } from "../api/types";
import { ALERT_TYPE_LABEL } from "../api/types";
import { useEventStream } from "../api/useEventStream";

const LEVEL_LABEL: Record<string, string> = { info: "提示", medium: "中", high: "高" };
const STATUS_LABEL: Record<string, string> = { open: "待处理", acknowledged: "已确认", closed: "已关闭" };

export function AlertsPage() {
  const queryClient = useQueryClient();
  const [type, setType] = useState("");
  const [level, setLevel] = useState("");
  const [status, setStatus] = useState("open");

  const params = new URLSearchParams({ page_size: "100" });
  if (type) params.set("alert_type", type);
  if (level) params.set("level", level);
  if (status) params.set("status", status);

  const list = useQuery({
    queryKey: ["alerts", type, level, status],
    queryFn: () => apiGet<Paginated<Alert>>(`/telematics/alerts?${params.toString()}`),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["alerts"] });

  // 新报警到达即刷新
  useEventStream((e) => {
    if (e.type === "alert") invalidate();
  });

  const act = useMutation({
    mutationFn: (v: { id: string; action: "ack" | "close" }) => apiPost(`/telematics/alerts/${v.id}/${v.action}`, {}),
    onSuccess: invalidate,
  });

  const items = list.data?.items ?? [];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">筛选</div>
        <div className="form-row">
          <select value={type} onChange={(e) => setType(e.target.value)}>
            <option value="">全部类型</option>
            {Object.entries(ALERT_TYPE_LABEL).map(([k, v]) => (
              <option key={k} value={k}>{v}</option>
            ))}
          </select>
          <select value={level} onChange={(e) => setLevel(e.target.value)}>
            <option value="">全部等级</option>
            <option value="high">高</option>
            <option value="medium">中</option>
            <option value="info">提示</option>
          </select>
          <select value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">全部状态</option>
            <option value="open">待处理</option>
            <option value="acknowledged">已确认</option>
            <option value="closed">已关闭</option>
          </select>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">报警中心</div>
        {list.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : items.length === 0 ? (
          <div className="muted" style={{ padding: 16 }}>暂无报警</div>
        ) : (
          <table className="table">
            <thead>
              <tr><th>类型</th><th>等级</th><th>车牌</th><th>运单</th><th>消息</th><th>触发时间</th><th>状态</th><th>操作</th></tr>
            </thead>
            <tbody>
              {items.map((a) => (
                <tr key={a.id}>
                  <td>{ALERT_TYPE_LABEL[a.alert_type]}</td>
                  <td><span className={`tag tag-${a.level === "high" ? "high" : a.level === "medium" ? "medium" : "low"}`}>{LEVEL_LABEL[a.level]}</span></td>
                  <td className="mono">{a.vehicle_plate || "-"}</td>
                  <td>{a.waybill_no ? <Link className="link mono" to={`/waybills/${a.waybill_no}`}>{a.waybill_no}</Link> : "-"}</td>
                  <td>{a.message}</td>
                  <td className="small">{new Date(a.triggered_at).toLocaleString()}</td>
                  <td>{STATUS_LABEL[a.status]}</td>
                  <td>
                    {a.status === "open" && (
                      <button className="btn-ghost" disabled={act.isPending} onClick={() => act.mutate({ id: a.id, action: "ack" })}>确认</button>
                    )}
                    {a.status !== "closed" && (
                      <button className="btn-ghost" disabled={act.isPending} onClick={() => act.mutate({ id: a.id, action: "close" })}>关闭</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
