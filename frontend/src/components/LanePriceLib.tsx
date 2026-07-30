import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import { useServerTable } from "../api/useServerTable";
import type { Carrier, CarrierLanePrice, Paginated } from "../api/types";
import { DataTable, type DataColumn } from "./DataTable";
import { FilterBuilder, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "./FilterBuilder";
import { StateView } from "./StateView";

const LANE_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "origin", label: "始发城市", type: "text", accessor: (l) => (l as CarrierLanePrice).origin_city },
  { key: "dest", label: "目的城市", type: "text", accessor: (l) => (l as CarrierLanePrice).dest_city },
  { key: "carrier", label: "承运商", type: "text", accessor: (l) => (l as CarrierLanePrice).carrier_name || "" },
  { key: "vehicle", label: "车型", type: "text", accessor: (l) => (l as CarrierLanePrice).vehicle_type || "" },
  { key: "standard", label: "标准价", type: "number", accessor: (l) => Number((l as CarrierLanePrice).standard_price) || 0 },
  { key: "last", label: "最近成交价", type: "number", accessor: (l) => Number((l as CarrierLanePrice).last_deal_price) || 0 },
  { key: "flag", label: "标记", type: "enum", options: [{ value: "recommended", label: "推荐" }, { value: "preferred", label: "常用" }, { value: "none", label: "无" }], accessor: (l) => ((l as CarrierLanePrice).is_recommended ? "recommended" : (l as CarrierLanePrice).is_preferred ? "preferred" : "none") },
];

const BLANK = {
  carrier: "", origin_city: "", dest_city: "", vehicle_type: "", vehicle_length_m: "",
  standard_price: "", min_price: "", max_price: "", last_deal_price: "",
  is_preferred: false, is_recommended: false, note: "",
};

export function LanePriceLib() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [form, setForm] = useState<typeof BLANK>({ ...BLANK });
  const [adding, setAdding] = useState(false);
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showFilter, setShowFilter] = useState(false);
  const laneActiveCount = activeConditionCount(model, LANE_FILTER_FIELDS);
  const anyFilter = Boolean(search) || laneActiveCount > 0;

  const carriers = useQuery({ queryKey: ["lp-carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=300") });
  const st = useServerTable<CarrierLanePrice>({
    queryKey: ["lane-prices"], path: "/carrier-lane-prices", pageSize: 50,
    defaultSort: { field: "origin_city", dir: "asc" }, model, search,
  });

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
      st.refetch();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const set = (k: keyof typeof BLANK, v: string | boolean) => setForm((f) => ({ ...f, [k]: v }));
  const canSubmit = form.carrier && form.origin_city.trim() && form.dest_city.trim() && form.standard_price;

  const laneColumns: DataColumn<CarrierLanePrice>[] = [
    { key: "lane", header: "线路", width: 130, alwaysVisible: true, sortField: "origin_city", sortValue: (l) => `${l.origin_city}${l.dest_city}`, exportValue: (l) => `${l.origin_city}→${l.dest_city}`, render: (l) => <>{l.origin_city}→{l.dest_city}</> },
    { key: "carrier", header: "承运商", width: 150, sortField: "carrier__name", sortValue: (l) => l.carrier_name || "", exportValue: (l) => l.carrier_name || "", render: (l) => l.carrier_name },
    { key: "vehicle", header: "车型/车长", width: 120, sortField: "vehicle_type", exportValue: (l) => `${l.vehicle_type || ""}${l.vehicle_length_m ? ` ${l.vehicle_length_m}m` : ""}`, render: (l) => <span className="small">{l.vehicle_type || "—"}{l.vehicle_length_m ? ` ${l.vehicle_length_m}m` : ""}</span> },
    { key: "standard", header: "标准价", width: 100, align: "right", sortField: "standard_price", sortValue: (l) => Number(l.standard_price) || 0, exportValue: (l) => Number(l.standard_price) || 0, render: (l) => <>{fmtMoney(l.standard_price)}</> },
    { key: "band", header: "区间", width: 150, align: "right", exportValue: (l) => `${l.min_price}~${l.max_price}`, render: (l) => <span className="small">{Number(l.min_price) > 0 || Number(l.max_price) > 0 ? `${fmtMoney(Number(l.min_price))}~${fmtMoney(Number(l.max_price))}` : "—"}</span> },
    { key: "last", header: "最近成交", width: 110, align: "right", sortField: "last_deal_price", sortValue: (l) => Number(l.last_deal_price) || 0, exportValue: (l) => Number(l.last_deal_price) || 0, render: (l) => <>{Number(l.last_deal_price) > 0 ? fmtMoney(Number(l.last_deal_price)) : "—"}</> },
    { key: "flag", header: "标记", width: 80, sortField: "flag_code", sortValue: (l) => (l.is_recommended ? "0" : l.is_preferred ? "1" : "2"), exportValue: (l) => (l.is_recommended ? "推荐" : l.is_preferred ? "常用" : ""), render: (l) => l.is_recommended ? <span className="tag tag-low">推荐</span> : l.is_preferred ? <span className="tag tag-info">常用</span> : <span className="muted">—</span> },
    { key: "eff", header: "有效期", width: 100, exportValue: (l) => (l.effective_to ? `至 ${l.effective_to}` : "长期"), render: (l) => <span className="small">{l.effective_to ? `至 ${l.effective_to}` : "长期"}</span> },
  ];

  return (
    <div className="panel om-panel">
      {laneActiveCount > 0 && (
        <div className="om-chips">
          <span className="muted small">条件（{model.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
          {model.conditions.map((c) => {
            const label = describeCondition(c, LANE_FILTER_FIELDS);
            if (!label) return null;
            return <span key={c.id} className="filter-chip"><span className="filter-chip-label" title={label}>{label}</span><button type="button" aria-label={`删除条件：${label}`} onClick={() => setModel((m) => ({ ...m, conditions: m.conditions.filter((x) => x.id !== c.id) }))}>×</button></span>;
          })}
          <button className="linkish small" onClick={() => setModel(EMPTY_MODEL)}>清空条件</button>
        </div>
      )}

      {adding && (
        <div className="form-section">
          <div className="section-label">新增线路价库条目</div>
          <div className="grid-form">
            <label>承运商 *
              <select value={form.carrier} onChange={(e) => set("carrier", e.target.value)}>
                <option value="">选择承运商</option>
                {(carriers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}{c.city ? ` · ${c.city}` : ""}</option>)}
              </select>
            </label>
            <label>起点城市 *<input value={form.origin_city} onChange={(e) => set("origin_city", e.target.value)} placeholder="如 上海" /></label>
            <label>终点城市 *<input value={form.dest_city} onChange={(e) => set("dest_city", e.target.value)} placeholder="如 杭州" /></label>
            <label>车型<input value={form.vehicle_type} onChange={(e) => set("vehicle_type", e.target.value)} placeholder="如 高栏/厢式" /></label>
            <label>车长(米)<input inputMode="decimal" value={form.vehicle_length_m} onChange={(e) => set("vehicle_length_m", e.target.value)} placeholder="如 13" /></label>
            <label>标准价 *<input inputMode="decimal" value={form.standard_price} onChange={(e) => set("standard_price", e.target.value)} placeholder="¥" /></label>
            <label>最低价<input inputMode="decimal" value={form.min_price} onChange={(e) => set("min_price", e.target.value)} placeholder="¥" /></label>
            <label>最高价<input inputMode="decimal" value={form.max_price} onChange={(e) => set("max_price", e.target.value)} placeholder="¥" /></label>
            <label>最近成交价<input inputMode="decimal" value={form.last_deal_price} onChange={(e) => set("last_deal_price", e.target.value)} placeholder="¥" /></label>
            <label className="check-label"><input type="checkbox" checked={form.is_preferred} onChange={(e) => set("is_preferred", e.target.checked)} />常用</label>
            <label className="check-label"><input type="checkbox" checked={form.is_recommended} onChange={(e) => set("is_recommended", e.target.checked)} />推荐</label>
            <label>备注<input value={form.note} onChange={(e) => set("note", e.target.value)} placeholder="议价空间 / 排队风险等" /></label>
          </div>
          <div className="form-actions">
            <button className="btn-primary" disabled={!canSubmit || create.isPending} onClick={() => create.mutate()}
              title={canSubmit ? "保存线路价" : "请补全：承运商 / 起点 / 终点 / 标准价"}>{create.isPending ? "保存中…" : "保存"}</button>
            <button className="btn-ghost" onClick={() => { setForm({ ...BLANK }); setAdding(false); }}>取消</button>
            {!canSubmit && <span className="muted small" style={{ alignSelf: "center", color: "var(--amber)" }}>▸ 带 * 为必填：承运商 / 起点 / 终点 / 标准价</span>}
          </div>
        </div>
      )}

      {st.isError ? (
        <StateView kind="error" onRetry={() => st.refetch()} />
      ) : (
        <DataTable<CarrierLanePrice>
          columns={laneColumns} rows={st.rows} rowKey={(l) => l.id} viewKey="lane-prices" exportName="线路价库"
          stickyFirst server={st.server} fill hideExport
          emptyState={anyFilter
            ? <StateView kind="empty" title="没有匹配的价库条目" hint="调整搜索/筛选条件再试。" />
            : <StateView kind="empty" title="暂无价库条目" hint="维护后，调度台可直接按线路比价选承运商。" />}
          toolbarLeft={
            <>
              <span className="om-title" style={{ marginRight: 2 }}>线路承运商价库<span className="ai-pill">{st.total}</span></span>
              <input className="search" style={{ minWidth: 180, flex: 1, maxWidth: 260 }} placeholder="搜索线路 / 承运商 / 车型" value={search} onChange={(e) => setSearch(e.target.value)} />
              <div style={{ position: "relative" }}>
                <button className={`btn-ghost${laneActiveCount > 0 || showFilter ? " on-accent" : ""}`} onClick={() => setShowFilter((v) => !v)}>
                  <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                    高级筛选{laneActiveCount > 0 ? ` · ${laneActiveCount}` : ""}
                  </span>
                </button>
                {showFilter && <FilterBuilder fields={LANE_FILTER_FIELDS} model={model} onChange={setModel} onClose={() => setShowFilter(false)} />}
              </div>
            </>
          }
          toolbarRight={
            <>
              {anyFilter && <button className="linkish small" onClick={() => { setSearch(""); setModel(EMPTY_MODEL); }}>重置</button>}
              <button className="btn-primary" onClick={() => setAdding((v) => !v)}>{adding ? "收起" : "+ 新增价库"}</button>
            </>
          }
        />
      )}
    </div>
  );
}
