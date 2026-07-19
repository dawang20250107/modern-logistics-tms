import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { toast } from "../api/toast";
import type { Carrier, CarrierLanePrice, Paginated } from "../api/types";
import { DataTable, type DataColumn } from "./DataTable";
import { StateView } from "./StateView";

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

  const laneColumns: DataColumn<CarrierLanePrice>[] = [
    { key: "lane", header: "线路", width: 130, alwaysVisible: true, sortValue: (l) => `${l.origin_city}${l.dest_city}`, exportValue: (l) => `${l.origin_city}→${l.dest_city}`, render: (l) => <>{l.origin_city}→{l.dest_city}</> },
    { key: "carrier", header: "承运商", width: 150, sortValue: (l) => l.carrier_name || "", exportValue: (l) => l.carrier_name || "", render: (l) => l.carrier_name },
    { key: "vehicle", header: "车型/车长", width: 120, exportValue: (l) => `${l.vehicle_type || ""}${l.vehicle_length_m ? ` ${l.vehicle_length_m}m` : ""}`, render: (l) => <span className="small">{l.vehicle_type || "—"}{l.vehicle_length_m ? ` ${l.vehicle_length_m}m` : ""}</span> },
    { key: "standard", header: "标准价", width: 100, align: "right", sortValue: (l) => Number(l.standard_price) || 0, exportValue: (l) => Number(l.standard_price) || 0, render: (l) => <>¥{Number(l.standard_price).toLocaleString()}</> },
    { key: "band", header: "区间", width: 130, align: "right", exportValue: (l) => `${l.min_price}~${l.max_price}`, render: (l) => <span className="small">{Number(l.min_price) > 0 || Number(l.max_price) > 0 ? `¥${Number(l.min_price).toLocaleString()}~${Number(l.max_price).toLocaleString()}` : "—"}</span> },
    { key: "last", header: "最近成交", width: 100, align: "right", sortValue: (l) => Number(l.last_deal_price) || 0, exportValue: (l) => Number(l.last_deal_price) || 0, render: (l) => <>{Number(l.last_deal_price) > 0 ? `¥${Number(l.last_deal_price).toLocaleString()}` : "—"}</> },
    { key: "flag", header: "标记", width: 80, sortValue: (l) => (l.is_recommended ? "0" : l.is_preferred ? "1" : "2"), exportValue: (l) => (l.is_recommended ? "推荐" : l.is_preferred ? "常用" : ""), render: (l) => l.is_recommended ? <span className="tag tag-low">推荐</span> : l.is_preferred ? <span className="tag tag-info">常用</span> : <span className="muted">—</span> },
    { key: "eff", header: "有效期", width: 100, exportValue: (l) => (l.effective_to ? `至 ${l.effective_to}` : "长期"), render: (l) => <span className="small">{l.effective_to ? `至 ${l.effective_to}` : "长期"}</span> },
  ];

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
        <StateView kind="loading" compact />
      ) : lanes.isError ? (
        <StateView kind="error" onRetry={() => lanes.refetch()} />
      ) : rows.length === 0 ? (
        <StateView kind="empty" title="暂无价库条目" hint="维护后，调度台可直接按线路比价选承运商。" />
      ) : (
        <DataTable<CarrierLanePrice>
          columns={laneColumns}
          rows={rows}
          rowKey={(l) => l.id}
          viewKey="lane-prices"
          exportName="线路价库"
          stickyFirst
          toolbarLeft={<span className="muted small">共 {rows.length} 条 · 点击表头排序 · 「列」增减字段</span>}
        />
      )}
    </div>
  );
}
