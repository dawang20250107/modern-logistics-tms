import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiDownload, apiGet, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { toast } from "../api/toast";
import type { Order, OrderChannel, Paginated } from "../api/types";
import { ORDER_CHANNEL_LABEL, ORDER_STATUS_LABEL, SLA_STATUS_LABEL } from "../api/types";
import { EmptyState } from "../components/EmptyState";
import { StructuredOrderForm } from "../components/StructuredOrderForm";

export function OrderIntakePage() {
  const queryClient = useQueryClient();
  const [statusFilter, setStatusFilter] = useState("");
  const [channelFilter, setChannelFilter] = useState("");
  const [search, setSearch] = useState("");
  const filterQs = `${statusFilter ? `&status=${statusFilter}` : ""}${channelFilter ? `&channel=${channelFilter}` : ""}${search ? `&search=${encodeURIComponent(search)}` : ""}`;
  const orders = useQuery({
    queryKey: ["orders", statusFilter, channelFilter, search],
    queryFn: () => apiGet<Paginated<Order>>(`/orders?page_size=50&ordering=-created_at${filterQs}`),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["orders"] });

  const ACTION_LABEL: Record<string, string> = { confirm: "已确认", convert: "已转运单" };
  const act = useMutation({
    mutationFn: (v: { id: string; action: string }) => apiPost(`/orders/${v.id}/${v.action}`, {}),
    onSuccess: (_d, v) => { toast.success(ACTION_LABEL[v.action] ?? "操作成功"); invalidate(); },
  });
  const clone = useMutation({
    mutationFn: (id: string) => apiPost<Order>(`/orders/${id}/clone`, {}),
    onSuccess: (o) => { toast.success(`已复制为草稿：${o.order_no}`); invalidate(); },
  });
  const merge = useMutation({
    mutationFn: (ids: string[]) => apiPost<Order>("/orders/merge", { ids }),
    onSuccess: (o) => { toast.success(`已合单：${o.order_no}`); setSelected(new Set()); invalidate(); },
  });

  const [selected, setSelected] = useState<Set<string>>(new Set());
  const toggle = (id: string) =>
    setSelected((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const BATCH_LABEL: Record<string, string> = { confirm: "确认", pool: "进池", cancel: "取消", delete: "删除" };
  const batch = useMutation({
    mutationFn: (v: { action: string; ids: string[] }) =>
      apiPost<{ ok_count: number; failed: Array<{ order_no: string; error: string }> }>("/orders/batch", v),
    onSuccess: (r, v) => {
      const failN = r.failed?.length ?? 0;
      toast.success(`批量${BATCH_LABEL[v.action]}完成：成功 ${r.ok_count}${failN ? `，失败 ${failN}` : ""}`);
      setSelected(new Set());
      invalidate();
    },
  });
  const runBatch = async (action: string) => {
    const ids = [...selected];
    if (ids.length === 0) return;
    if (action === "cancel" || action === "delete") {
      const ok = await confirmAction({
        message: `确定批量${BATCH_LABEL[action]} ${ids.length} 个订单？此操作不可恢复。`,
        tone: "danger",
        confirmText: `批量${BATCH_LABEL[action]}`,
      });
      if (!ok) return;
    }
    batch.mutate({ action, ids });
  };

  const items = orders.data?.items ?? [];
  const total = orders.data?.total ?? 0;

  return (
    <div className="stack">
      <StructuredOrderForm onCreated={invalidate} />

      <div className="panel">
        <div className="panel-head">
          订单 · {total}
          <button className="btn-ghost" onClick={() => apiDownload(`/orders/export?page_size=5000${filterQs}`, "orders.csv")}>导出 CSV</button>
        </div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
          <button className={`chip${statusFilter === "" ? " chip-on" : ""}`} onClick={() => setStatusFilter("")}>全部</button>
          {["draft", "pending_confirm", "confirmed", "pooled", "dispatching", "converted", "completed", "cancelled"].map((s) => (
            <button key={s} className={`chip${statusFilter === s ? " chip-on" : ""}`} onClick={() => setStatusFilter(s)}>
              {ORDER_STATUS_LABEL[s] ?? s}
            </button>
          ))}
          <input
            className="search"
            style={{ minWidth: 200, flex: 1 }}
            placeholder="搜索订单号 / 电话 / 始发 / 目的地"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8, paddingTop: 0 }}>
          <span className="muted small">来源</span>
          <button className={`chip${channelFilter === "" ? " chip-on" : ""}`} onClick={() => setChannelFilter("")}>全部</button>
          {Object.entries(ORDER_CHANNEL_LABEL).map(([k, v]) => (
            <button key={k} className={`chip${channelFilter === k ? " chip-on" : ""}`} onClick={() => setChannelFilter(k)}>{v}</button>
          ))}
        </div>
        {selected.size > 0 && (
          <div className="batch-bar">
            <span>已选 {selected.size} 单</span>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("confirm")}>批量确认</button>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("pool")}>批量进池</button>
            <button className="btn-ghost" disabled={merge.isPending || selected.size < 2} onClick={async () => {
              if (await confirmAction({ message: `将选中的 ${selected.size} 张订单合并为一张？原单作废。`, confirmText: "合单" })) merge.mutate([...selected]);
            }}>合单</button>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("cancel")}>批量取消</button>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("delete")}>批量删除</button>
            <button className="btn-ghost" onClick={() => setSelected(new Set())}>清除选择</button>
          </div>
        )}
        {orders.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : items.length === 0 ? (
          <EmptyState
            title={statusFilter || search ? "没有匹配的订单" : "暂无订单"}
            hint={statusFilter || search ? "试试调整状态过滤或搜索条件" : "使用上方表单创建订单"}
          />
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 36 }}>
                  <input
                    type="checkbox"
                    checked={items.length > 0 && items.every((o) => selected.has(o.id))}
                    onChange={(e) => setSelected(e.target.checked ? new Set(items.map((o) => o.id)) : new Set())}
                  />
                </th>
                <th>订单号</th><th>渠道</th><th>线路</th><th>货量</th><th>状态</th><th>SLA</th><th>操作</th>
              </tr>
            </thead>
            <tbody>
              {items.map((o) => (
                <tr key={o.id} style={selected.has(o.id) ? { background: "#f1f5fb" } : {}}>
                  <td><input type="checkbox" checked={selected.has(o.id)} onChange={() => toggle(o.id)} /></td>
                  <td className="mono"><Link className="link" to={`/orders/${o.id}`}>{o.order_no}</Link></td>
                  <td>{ORDER_CHANNEL_LABEL[o.channel]}</td>
                  <td>{o.origin} → {o.destination}</td>
                  <td>{o.cargo_weight_ton}吨 / {o.cargo_quantity}件</td>
                  <td><span className={`tag tag-${o.status === "converted" || o.status === "completed" ? "low" : o.status === "cancelled" ? "none" : "medium"}`}>{ORDER_STATUS_LABEL[o.status] ?? o.status}</span></td>
                  <td><span className={`tag tag-sla_${o.sla_status}`}>{SLA_STATUS_LABEL[o.sla_status] ?? o.sla_status}</span></td>
                  <td className="row-actions">
                    {(o.status === "draft" || o.status === "pending_confirm") && (
                      <button className="btn-ghost" disabled={act.isPending} onClick={() => act.mutate({ id: o.id, action: "confirm" })}>确认</button>
                    )}
                    {(o.status === "pending_confirm" || o.status === "confirmed") && (
                      <button className="btn-ghost" disabled={act.isPending} onClick={() => act.mutate({ id: o.id, action: "convert" })}>转运单</button>
                    )}
                    <button className="btn-ghost" disabled={clone.isPending} onClick={() => clone.mutate(o.id)}>复制</button>
                    {(o.waybill_nos ?? []).map((no) => (
                      <Link key={no} className="link mono small" to={`/waybills/${no}`} style={{ marginLeft: 6 }}>{no}</Link>
                    ))}
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
