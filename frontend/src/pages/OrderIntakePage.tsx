import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { toast } from "../api/toast";
import type { DuplicateOrder, Order, OrderChannel, Paginated, ParsedOrder } from "../api/types";
import { ORDER_CHANNEL_LABEL, ORDER_STATUS_LABEL, SLA_STATUS_LABEL } from "../api/types";

type Fields = Record<string, string | number>;

const FIELD_LABELS: Array<[string, string]> = [
  ["origin", "始发地"],
  ["destination", "目的地"],
  ["contact_phone", "联系电话"],
  ["cargo_desc", "货物"],
  ["cargo_quantity", "件数"],
  ["cargo_weight_ton", "吨位"],
  ["cargo_volume_cbm", "体积(方)"],
];

export function OrderIntakePage() {
  const queryClient = useQueryClient();
  const [channel, setChannel] = useState<OrderChannel>("wechat_group");
  const [source, setSource] = useState("");
  const [text, setText] = useState("");
  const [fields, setFields] = useState<Fields>({});
  const [parseSource, setParseSource] = useState("");
  const [missing, setMissing] = useState<Array<{ field: string; label: string }>>([]);
  const [duplicates, setDuplicates] = useState<DuplicateOrder[]>([]);

  const [statusFilter, setStatusFilter] = useState("");
  const [search, setSearch] = useState("");
  const orderQuery = `/orders?page_size=50&ordering=-created_at${statusFilter ? `&status=${statusFilter}` : ""}${search ? `&search=${encodeURIComponent(search)}` : ""}`;
  const orders = useQuery({
    queryKey: ["orders", statusFilter, search],
    queryFn: () => apiGet<Paginated<Order>>(orderQuery),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["orders"] });

  const parse = useMutation({
    mutationFn: () => apiPost<ParsedOrder>("/orders/parse-preview", { text }),
    onSuccess: (data) => {
      setFields(data.fields ?? {});
      setParseSource(data.meta?.source ?? "");
      setMissing(data.missing ?? []);
      setDuplicates(data.duplicates ?? []);
    },
  });

  const submit = useMutation({
    mutationFn: () => apiPost<Order>("/orders/intake", { channel, source, fields, text }),
    onSuccess: (o) => {
      setText("");
      setFields({});
      setParseSource("");
      setMissing([]);
      setDuplicates([]);
      toast.success(`建单成功：${o.order_no}`);
      invalidate();
    },
  });

  const ACTION_LABEL: Record<string, string> = { confirm: "已确认", convert: "已转运单" };
  const act = useMutation({
    mutationFn: (v: { id: string; action: string }) => apiPost(`/orders/${v.id}/${v.action}`, {}),
    onSuccess: (_d, v) => {
      toast.success(ACTION_LABEL[v.action] ?? "操作成功");
      invalidate();
    },
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

  const [bulk, setBulk] = useState("");
  const importMut = useMutation({
    mutationFn: () => {
      const rows = bulk.split("\n").map((l) => l.trim()).filter(Boolean).map((line) => {
        const [origin, destination, weight, qty, phone] = line.split(/[,，\t]/).map((s) => s.trim());
        return {
          origin, destination,
          cargo_weight_ton: weight ? Number(weight) : undefined,
          cargo_quantity: qty ? Number(qty) : undefined,
          contact_phone: phone || undefined,
        };
      });
      return apiPost<{ ok_count: number; failed_count: number }>("/orders/import", { channel, source, rows });
    },
    onSuccess: (r) => {
      setBulk("");
      toast.success(`批量建单完成：成功 ${r.ok_count}，失败 ${r.failed_count}`);
      invalidate();
    },
  });

  const items = orders.data?.items ?? [];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          AI 多渠道建单
          {parseSource && <span className="ai-pill">{parseSource === "deepseek" ? "AI 解析" : "智能解析"}</span>}
        </div>
        <div className="form-row">
          <select value={channel} onChange={(e) => setChannel(e.target.value as OrderChannel)}>
            {Object.entries(ORDER_CHANNEL_LABEL).map(([k, v]) => (
              <option key={k} value={k}>{v}</option>
            ))}
          </select>
          <input placeholder="来源（群名/坐席，可选）" value={source} onChange={(e) => setSource(e.target.value)} />
        </div>
        <div style={{ padding: "0 18px 14px" }}>
          <textarea
            className="search"
            style={{ width: "100%", minHeight: 84, resize: "vertical" }}
            placeholder="粘贴客户消息，例如：上海到成都，10吨货，5件，电话13800001234"
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
          <div style={{ display: "flex", gap: 10, marginTop: 10 }}>
            <button className="btn-ghost" disabled={!text || parse.isPending} onClick={() => parse.mutate()}>
              {parse.isPending ? "解析中…" : "AI 解析"}
            </button>
            <button className="btn-primary" disabled={submit.isPending} onClick={() => submit.mutate()}>
              {submit.isPending ? "建单中…" : "建单（待确认）"}
            </button>
          </div>
        </div>

        {Object.keys(fields).length > 0 && (
          <div className="kv">
            {FIELD_LABELS.map(([key, label]) => (
              <div key={key}>
                <span>{label}</span>
                <input
                  className="search"
                  style={{ minWidth: 0, width: 160 }}
                  value={String(fields[key] ?? "")}
                  onChange={(e) => setFields({ ...fields, [key]: e.target.value })}
                />
              </div>
            ))}
          </div>
        )}

        {missing.length > 0 && (
          <div style={{ padding: "0 18px 16px", display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <span className="muted small">🤖 AI 建议补充：</span>
            {missing.map((m) => (
              <span key={m.field} className="tag tag-medium">{m.label}</span>
            ))}
          </div>
        )}

        {duplicates.length > 0 && (
          <div style={{ padding: "0 18px 16px" }}>
            <div className="tag tag-high" style={{ marginBottom: 8 }}>⚠ 疑似重复下单（近 24h 同电话/同线路 {duplicates.length} 单）</div>
            <div className="stack" style={{ gap: 6 }}>
              {duplicates.map((d) => (
                <Link key={d.id} to={`/orders/${d.id}`} className="link mono small">
                  {d.order_no} · {d.origin}→{d.destination} · {ORDER_STATUS_LABEL[d.status] ?? d.status} · {new Date(d.created_at).toLocaleString()}
                </Link>
              ))}
            </div>
          </div>
        )}
      </div>

      <div className="panel">
        <div className="panel-head">
          批量导入
          {importMut.data && (
            <span className="muted small">成功 {importMut.data.ok_count} · 失败 {importMut.data.failed_count}</span>
          )}
        </div>
        <div style={{ padding: "12px 18px 16px" }}>
          <textarea
            className="search"
            style={{ width: "100%", minHeight: 84, resize: "vertical" }}
            placeholder="每行一单：始发,目的,吨位,件数,电话（逗号/制表符分隔）&#10;例：上海,成都,10,5,13800001234"
            value={bulk}
            onChange={(e) => setBulk(e.target.value)}
          />
          <button className="btn-primary" style={{ marginTop: 10 }} disabled={!bulk.trim() || importMut.isPending} onClick={() => importMut.mutate()}>
            {importMut.isPending ? "导入中…" : "批量建单"}
          </button>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">订单（全流程）</div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
          <button className={`chip${statusFilter === "" ? " chip-on" : ""}`} onClick={() => setStatusFilter("")}>全部</button>
          {["pending_confirm", "confirmed", "pooled", "dispatching", "converted", "completed", "cancelled"].map((s) => (
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
        {selected.size > 0 && (
          <div className="batch-bar">
            <span>已选 {selected.size} 单</span>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("confirm")}>批量确认</button>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("pool")}>批量进池</button>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("cancel")}>批量取消</button>
            <button className="btn-ghost" disabled={batch.isPending} onClick={() => runBatch("delete")}>批量删除</button>
            <button className="btn-ghost" onClick={() => setSelected(new Set())}>清除选择</button>
          </div>
        )}
        {orders.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : items.length === 0 ? (
          <div className="muted" style={{ padding: 16 }}>暂无订单</div>
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
                  <td>
                    {o.status === "pending_confirm" && (
                      <button className="btn-ghost" disabled={act.isPending} onClick={() => act.mutate({ id: o.id, action: "confirm" })}>确认</button>
                    )}
                    {(o.status === "pending_confirm" || o.status === "confirmed") && (
                      <button className="btn-ghost" disabled={act.isPending} onClick={() => act.mutate({ id: o.id, action: "convert" })}>转运单</button>
                    )}
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
