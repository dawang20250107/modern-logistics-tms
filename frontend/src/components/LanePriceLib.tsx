import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { toast } from "../api/toast";
import type { Carrier, CarrierLanePrice, Paginated } from "../api/types";

const BLANK = {
  carrier: "", origin_city: "", dest_city: "", vehicle_type: "", vehicle_length_m: "",
  standard_price: "", min_price: "", max_price: "", last_deal_price: "",
  is_preferred: false, is_recommended: false, note: "",
};

export function LanePriceLib() {
  const qc = useQueryClient();
  const [kw, setKw] = useState("");
  const [form, setForm] = useState<typeof BLANK>({ ...BLANK });
  const [adding, setAdding] = useState(false);

  const carriers = useQuery({ queryKey: ["lp-carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=300") });
  const lanes = useQuery({ queryKey: ["lane-prices"], queryFn: () => apiGet<Paginated<CarrierLanePrice>>("/carrier-lane-prices?page_size=300") });

  const rows = useMemo(() => {
    const items = lanes.data?.items ?? [];
    const k = kw.trim().toLowerCase();
    return k ? items.filter((l) => `${l.origin_city} ${l.dest_city} ${l.carrier_name ?? ""} ${l.vehicle_type ?? ""}`.toLowerCase().includes(k)) : items;
  }, [lanes.data, kw]);

  const create = useMutation({
    mutationFn: () => apiPost<CarrierLanePrice>("/carrier-lane-prices", {
      carrier: form.carrier,
      origin_city: form.origin_city.trim(),
      dest_city: form.dest_city.trim(),
      vehicle_type: form.vehicle_type.trim(),
      vehicle_length_m: form.vehicle_length_m || 0,
      standard_price: form.standard_price || 0,
      min_price: form.min_price || 0,
      max_price: form.max_price || 0,
      last_deal_price: form.last_deal_price || 0,
      is_preferred: form.is_preferred,
      is_recommended: form.is_recommended,
      note: form.note.trim(),
    }),
    onSuccess: () => {
      toast.success("已加入线路价库");
      setForm({ ...BLANK });
      setAdding(false);
      qc.invalidateQueries({ queryKey: ["lane-prices"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const set = (k: keyof typeof BLANK, v: string | boolean) => setForm((f) => ({ ...f, [k]: v }));
  const canSubmit = form.carrier && form.origin_city.trim() && form.dest_city.trim() && form.standard_price;

  return (
    <div className="panel">
      <div className="panel-head">
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>线路承运商价库<span className="ai-pill">{rows.length}</span></span>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <input className="search" style={{ width: 240 }} placeholder="搜索线路 / 承运商 / 车型" value={kw} onChange={(e) => setKw(e.target.value)} />
          <button className="btn-primary" onClick={() => setAdding((v) => !v)}>{adding ? "收起" : "+ 新增价库"}</button>
        </div>
      </div>

      {adding && (
        <div className="form-section">
          <div className="section-label">新增线路价库条目</div>
          <div className="grid-form">
            <label>承运商
              <select value={form.carrier} onChange={(e) => set("carrier", e.target.value)}>
                <option value="">选择承运商</option>
                {(carriers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}{c.city ? ` · ${c.city}` : ""}</option>)}
              </select>
            </label>
            <label>起点城市<input value={form.origin_city} onChange={(e) => set("origin_city", e.target.value)} placeholder="如 上海" /></label>
            <label>终点城市<input value={form.dest_city} onChange={(e) => set("dest_city", e.target.value)} placeholder="如 杭州" /></label>
            <label>车型<input value={form.vehicle_type} onChange={(e) => set("vehicle_type", e.target.value)} placeholder="如 高栏/厢式" /></label>
            <label>车长(米)<input value={form.vehicle_length_m} onChange={(e) => set("vehicle_length_m", e.target.value)} placeholder="如 13" /></label>
            <label>标准价<input value={form.standard_price} onChange={(e) => set("standard_price", e.target.value)} placeholder="¥" /></label>
            <label>最低价<input value={form.min_price} onChange={(e) => set("min_price", e.target.value)} placeholder="¥" /></label>
            <label>最高价<input value={form.max_price} onChange={(e) => set("max_price", e.target.value)} placeholder="¥" /></label>
            <label>最近成交价<input value={form.last_deal_price} onChange={(e) => set("last_deal_price", e.target.value)} placeholder="¥" /></label>
            <label className="check-label"><input type="checkbox" checked={form.is_preferred} onChange={(e) => set("is_preferred", e.target.checked)} />常用</label>
            <label className="check-label"><input type="checkbox" checked={form.is_recommended} onChange={(e) => set("is_recommended", e.target.checked)} />推荐</label>
            <label>备注<input value={form.note} onChange={(e) => set("note", e.target.value)} placeholder="议价空间 / 排队风险等" /></label>
          </div>
          <div className="form-actions">
            <button className="btn-primary" disabled={!canSubmit || create.isPending} onClick={() => create.mutate()}>{create.isPending ? "保存中…" : "保存"}</button>
            <button className="btn-ghost" onClick={() => { setForm({ ...BLANK }); setAdding(false); }}>取消</button>
          </div>
        </div>
      )}

      {lanes.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : rows.length === 0 ? (
        <div className="muted" style={{ padding: 16 }}>暂无价库条目。维护后，调度台可直接按线路比价选承运商。</div>
      ) : (
        <table className="table">
          <thead><tr>
            <th>线路</th><th>承运商</th><th>车型/车长</th><th className="num">标准价</th><th className="num">区间</th><th className="num">最近成交</th><th>标记</th><th>有效期</th>
          </tr></thead>
          <tbody>
            {rows.map((l) => (
              <tr key={l.id}>
                <td>{l.origin_city}→{l.dest_city}</td>
                <td>{l.carrier_name}</td>
                <td className="small">{l.vehicle_type || "—"}{l.vehicle_length_m ? ` ${l.vehicle_length_m}m` : ""}</td>
                <td className="num">¥{Number(l.standard_price).toLocaleString()}</td>
                <td className="num small">{Number(l.min_price) > 0 || Number(l.max_price) > 0 ? `¥${Number(l.min_price).toLocaleString()}~${Number(l.max_price).toLocaleString()}` : "—"}</td>
                <td className="num">{Number(l.last_deal_price) > 0 ? `¥${Number(l.last_deal_price).toLocaleString()}` : "—"}</td>
                <td>{l.is_recommended ? <span className="tag tag-low">推荐</span> : l.is_preferred ? <span className="tag tag-info">常用</span> : "—"}</td>
                <td className="small">{l.effective_to ? `至 ${l.effective_to}` : "长期"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
