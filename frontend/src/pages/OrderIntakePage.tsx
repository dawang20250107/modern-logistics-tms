import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import type { Order, OrderChannel, Paginated, ParsedOrder } from "../api/types";
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

  const orders = useQuery({
    queryKey: ["orders"],
    queryFn: () => apiGet<Paginated<Order>>("/orders?page_size=50&ordering=-created_at"),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["orders"] });

  const parse = useMutation({
    mutationFn: () => apiPost<ParsedOrder>("/orders/parse-preview", { text }),
    onSuccess: (data) => {
      setFields(data.fields ?? {});
      setParseSource(data.meta?.source ?? "");
      setMissing(data.missing ?? []);
    },
  });

  const submit = useMutation({
    mutationFn: () => apiPost<Order>("/orders/intake", { channel, source, fields, text }),
    onSuccess: () => {
      setText("");
      setFields({});
      setParseSource("");
      invalidate();
    },
  });

  const act = useMutation({
    mutationFn: (v: { id: string; action: string }) => apiPost(`/orders/${v.id}/${v.action}`, {}),
    onSuccess: invalidate,
  });

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
    onSuccess: () => { setBulk(""); invalidate(); },
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
        {orders.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : items.length === 0 ? (
          <div className="muted" style={{ padding: 16 }}>暂无订单</div>
        ) : (
          <table className="table">
            <thead>
              <tr><th>订单号</th><th>渠道</th><th>线路</th><th>货量</th><th>状态</th><th>SLA</th><th>操作</th></tr>
            </thead>
            <tbody>
              {items.map((o) => (
                <tr key={o.id}>
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
