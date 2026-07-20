import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet } from "../api/client";
import { fmtMoney } from "../api/format";
import { useServerTable } from "../api/useServerTable";
import type { Carrier, CarrierLanePrice, Paginated } from "../api/types";
import { DataTable, type DataColumn } from "./DataTable";
import { FilterBuilder, activeConditionCount, describeCondition, EMPTY_MODEL, type FilterFieldDef, type FilterModel } from "./FilterBuilder";
import { StateView } from "./StateView";

const CARRIER_TYPE_LABEL: Record<string, string> = { owner_fleet: "个体车队", company_fleet: "公司车队", platform: "网货平台", temporary: "临时承运商" };
const CARRIER_FILTER_FIELDS: FilterFieldDef[] = [
  { key: "name", label: "承运商", type: "text", accessor: (c) => (c as Carrier).name },
  { key: "code", label: "编码", type: "text", accessor: (c) => (c as Carrier).code },
  { key: "city", label: "城市", type: "text", accessor: (c) => (c as Carrier).city || "" },
  { key: "type", label: "类型", type: "enum", options: Object.entries(CARRIER_TYPE_LABEL).map(([value, label]) => ({ value, label })), accessor: (c) => (c as Carrier).carrier_type || "" },
  { key: "grade", label: "评级", type: "enum", options: ["A", "B", "C", "D"].map((v) => ({ value: v, label: `${v} 级` })), accessor: (c) => (c as Carrier).grade || "" },
  { key: "credit_days", label: "账期(天)", type: "number", accessor: (c) => Number((c as Carrier).credit_days) || 0 },
  { key: "blocked", label: "风控", type: "enum", options: [{ value: "1", label: "停派/黑名单/资质过期" }, { value: "0", label: "正常" }], accessor: (c) => ((c as Carrier).blacklisted || (c as Carrier).dispatch_blocked ? "1" : "0") },
  { key: "active", label: "状态", type: "enum", options: [{ value: "1", label: "启用" }, { value: "0", label: "停用" }], accessor: (c) => ((c as Carrier).is_active ? "1" : "0") },
];

function pct(n?: number): string {
  return n == null ? "—" : `${Math.round(n * 100)}%`;
}

function riskTag(c: Carrier) {
  if (c.blacklisted) return <span className="tag tag-high">黑名单</span>;
  if (c.dispatch_blocked) return <span className="tag tag-high">停派</span>;
  if (c.grade === "A") return <span className="tag tag-low">优质 A</span>;
  if (c.grade === "D") return <span className="tag tag-high">高风险 D</span>;
  return <span className="tag tag-low">正常</span>;
}

// 承运商详情抽屉：档案 + 风控合规 + 经营表现 + 线路价库
function CarrierDrawer({ carrierId, onClose }: { carrierId: string; onClose: () => void }) {
  const detail = useQuery({ queryKey: ["carrier", carrierId], queryFn: () => apiGet<Carrier>(`/carriers/${carrierId}`) });
  const lanes = useQuery({
    queryKey: ["carrier-lanes", carrierId],
    queryFn: () => apiGet<Paginated<CarrierLanePrice>>(`/carrier-lane-prices?carrier=${carrierId}&page_size=100`),
  });
  const c = detail.data;
  const perf = c?.performance;

  return (
    <div className="wb-overlay" onClick={onClose}>
      <div className="wb-drawer" onClick={(e) => e.stopPropagation()}>
        <div className="wb-drawer-head">
          <div>
            <div style={{ fontSize: 16, fontWeight: 650, display: "flex", alignItems: "center", gap: 8 }}>
              {c?.name ?? "…"}
              {c?.carrier_type_label && <span className="tag tag-info">{c.carrier_type_label}</span>}
              {c?.grade && <span className="tag tag-none">{c.grade_label ?? c.grade}</span>}
            </div>
            <div className="muted small mono" style={{ marginTop: 2 }}>{c?.code}</div>
          </div>
          <button className="btn-ghost" onClick={onClose}>关闭 [Esc]</button>
        </div>
        <div className="wb-drawer-body">
          {detail.isLoading ? (
            <StateView kind="loading" compact />
          ) : detail.isError ? (
            <StateView kind="error" onRetry={() => detail.refetch()} />
          ) : c ? (
            <div className="stack" style={{ gap: 16 }}>
              {c.dispatch_blocked && (
                <div className="tag tag-high" style={{ padding: "8px 12px", display: "block" }}>⛔ 当前不可派单：{c.dispatch_blocked}</div>
              )}

              <div>
                <div className="section-label">档案</div>
                <div className="kv">
                  <div><span>所在城市</span><b>{c.city || "—"}</b></div>
                  <div><span>服务区域</span><b>{c.service_area || "—"}</b></div>
                  <div><span>联系人</span><b>{c.contact_name || "—"} {c.contact_phone || ""}</b></div>
                  <div><span>结算方式</span><b>{c.settlement_type || "—"}</b></div>
                  <div><span>账期</span><b>{c.credit_days ?? "—"} 天 · 每月 {c.billing_day ?? "—"} 号出账</b></div>
                  <div><span>授信额度</span><b>{Number(c.credit_limit) > 0 ? `¥${Number(c.credit_limit).toLocaleString()}` : "不限"}</b></div>
                  <div><span>营业执照</span><b className="mono small">{c.business_license_no || "—"}</b></div>
                  <div><span>道路运输许可</span><b className="mono small">{c.transport_license_no || "—"}</b></div>
                  <div><span>开票税号</span><b className="mono small">{c.tax_no || "—"}</b></div>
                </div>
              </div>

              <div>
                <div className="section-label">风控与合规</div>
                <div className="kv">
                  <div><span>综合评级</span><b>{c.grade_label ?? c.grade ?? "—"}</b></div>
                  <div><span>黑名单</span><b style={{ color: c.blacklisted ? "var(--red)" : undefined }}>{c.blacklisted ? `是（${c.blacklist_reason || "—"}）` : "否"}</b></div>
                  <div><span>承运资质到期</span><b>{c.qualification_expiry || "—"}</b></div>
                  <div><span>合同有效期</span><b>{c.contract_expiry || "—"}</b></div>
                  <div><span>责任险到期</span><b>{c.insurance_expiry || "—"}</b></div>
                </div>
                {c.expiry_alerts && c.expiry_alerts.length > 0 && (
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 8 }}>
                    {c.expiry_alerts.map((a, i) => (
                      <span key={i} className={`tag tag-${a.expired ? "high" : "medium"}`}>{a.label}{a.expired ? "已过期" : "临期"} · {a.date}</span>
                    ))}
                  </div>
                )}
              </div>

              <div>
                <div className="section-label">经营表现（近 90 天）</div>
                {perf && perf.has_history ? (
                  <>
                    <div className="kv">
                      <div><span>成交票数</span><b>{perf.deals}</b></div>
                      <div><span>准班率</span><b>{pct(perf.on_time_rate)}</b></div>
                      <div><span>异常率</span><b style={{ color: perf.exception_rate >= 0.08 ? "var(--red)" : undefined }}>{pct(perf.exception_rate)}</b></div>
                      <div><span>回单及时率</span><b>{pct(perf.receipt_timely_rate)}</b></div>
                    </div>
                    {perf.frequent_routes && perf.frequent_routes.length > 0 && (
                      <div style={{ marginTop: 8 }}>
                        <div className="muted small" style={{ marginBottom: 4 }}>常跑线路</div>
                        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                          {perf.frequent_routes.map((r, i) => (
                            <span key={i} className="tag tag-info">{r.origin}→{r.destination} · {r.deals}单</span>
                          ))}
                        </div>
                      </div>
                    )}
                  </>
                ) : (
                  <div className="muted small">近 90 天暂无成交记录，建议先试单积累履约数据。</div>
                )}
              </div>

              <div>
                <div className="section-label">线路价库</div>
                {lanes.isLoading ? (
                  <StateView kind="loading" compact />
                ) : lanes.isError ? (
                  <StateView kind="error" onRetry={() => lanes.refetch()} compact />
                ) : (lanes.data?.items ?? []).length === 0 ? (
                  <StateView kind="empty" title="尚未维护线路价" hint="补充常跑线路价格后，调度推荐会优先引用价库。" compact />
                ) : (
                  <table className="table" style={{ fontSize: 12.5 }}>
                    <thead><tr><th>线路</th><th>车型</th><th className="num">标准价</th><th className="num">最近成交</th><th>标记</th></tr></thead>
                    <tbody>
                      {(lanes.data?.items ?? []).map((l) => (
                        <tr key={l.id}>
                          <td>{l.origin_city}→{l.dest_city}</td>
                          <td className="small">{l.vehicle_type || "—"}{l.vehicle_length_m ? ` ${l.vehicle_length_m}m` : ""}</td>
                          <td className="num">{fmtMoney(l.standard_price)}</td>
                          <td className="num">{Number(l.last_deal_price) > 0 ? `¥${Number(l.last_deal_price).toLocaleString()}` : "—"}</td>
                          <td>{l.is_recommended ? <span className="tag tag-low">推荐</span> : l.is_preferred ? <span className="tag tag-info">常用</span> : "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            </div>
          ) : (
            <div className="muted" style={{ padding: 16 }}>加载失败。</div>
          )}
        </div>
      </div>
    </div>
  );
}

export function CarrierCenter() {
  const [search, setSearch] = useState("");
  const [openId, setOpenId] = useState<string | null>(null);
  const [model, setModel] = useState<FilterModel>(EMPTY_MODEL);
  const [showFilter, setShowFilter] = useState(false);
  const ccActiveCount = activeConditionCount(model, CARRIER_FILTER_FIELDS);
  const anyFilter = Boolean(search) || ccActiveCount > 0;
  const st = useServerTable<Carrier>({
    queryKey: ["cc-carriers"], path: "/carriers", pageSize: 50,
    defaultSort: { field: "name", dir: "asc" }, model, search,
  });

  const carrierColumns: DataColumn<Carrier>[] = [
    { key: "name", header: "承运商", width: 200, alwaysVisible: true, sortField: "name", sortValue: (c) => c.name, exportValue: (c) => `${c.name} ${c.code}`, render: (c) => <><span className="link">{c.name}</span> <span className="muted small mono">{c.code}</span></> },
    { key: "type", header: "类型", width: 100, sortField: "carrier_type", sortValue: (c) => c.carrier_type_label || "", exportValue: (c) => c.carrier_type_label || "", render: (c) => <span className="small">{c.carrier_type_label || "—"}</span> },
    { key: "city", header: "城市", width: 90, sortField: "city", sortValue: (c) => c.city || "", exportValue: (c) => c.city || "", render: (c) => <span className="small">{c.city || "—"}</span> },
    { key: "risk", header: "评级/风控", width: 110, sortField: "grade", sortValue: (c) => (c.blacklisted ? "0" : c.grade || "z"), exportValue: (c) => (c.blacklisted ? "黑名单" : c.grade_label || c.grade || ""), render: (c) => riskTag(c) },
    { key: "credit", header: "账期", width: 80, align: "right", sortField: "credit_days", sortValue: (c) => c.credit_days ?? 0, exportValue: (c) => `${c.credit_days ?? ""}`, render: (c) => <>{c.credit_days ?? "—"}天</> },
    { key: "alerts", header: "到期预警", width: 110, sortValue: (c) => c.expiry_alerts?.length ?? 0, exportValue: (c) => `${c.expiry_alerts?.length ?? 0}`, render: (c) => (c.expiry_alerts && c.expiry_alerts.length > 0 ? <span className={`tag tag-${c.expiry_alerts.some((a) => a.expired) ? "high" : "medium"}`}>{c.expiry_alerts.length} 项临期</span> : <span className="muted small">—</span>) },
    { key: "active", header: "状态", width: 80, sortField: "is_active", sortValue: (c) => (c.is_active ? "1" : "0"), exportValue: (c) => (c.is_active ? "启用" : "停用"), render: (c) => <span className={`tag ${c.is_active ? "tag-low" : "tag-none"}`}>{c.is_active ? "启用" : "停用"}</span> },
  ];

  return (
    <div className="panel om-panel">
      {ccActiveCount > 0 && (
        <div className="om-chips">
          <span className="muted small">条件（{model.combinator === "and" ? "全部满足" : "任一满足"}）：</span>
          {model.conditions.map((c) => {
            const label = describeCondition(c, CARRIER_FILTER_FIELDS);
            if (!label) return null;
            return <span key={c.id} className="filter-chip">{label}<button onClick={() => setModel((m) => ({ ...m, conditions: m.conditions.filter((x) => x.id !== c.id) }))}>×</button></span>;
          })}
          <button className="linkish small" onClick={() => setModel(EMPTY_MODEL)}>清空条件</button>
        </div>
      )}
      {st.isError ? (
        <StateView kind="error" onRetry={() => st.refetch()} />
      ) : (
        <DataTable<Carrier>
          columns={carrierColumns} rows={st.rows} rowKey={(c) => c.id} viewKey="carriers" exportName="承运商"
          onRowClick={(c) => setOpenId(c.id)} stickyFirst server={st.server} fill hideExport
          emptyState={anyFilter ? <StateView kind="empty" title="没有匹配的承运商" hint="调整搜索/筛选条件再试。" /> : <StateView kind="empty" scene="carrier-empty" />}
          toolbarLeft={
            <>
              <span className="om-title" style={{ marginRight: 2 }}>承运资源池<span className="ai-pill">{st.total}</span></span>
              <input className="search" style={{ minWidth: 180, flex: 1, maxWidth: 280 }} placeholder="搜索承运商 / 城市 / 电话" value={search} onChange={(e) => setSearch(e.target.value)} />
              <div style={{ position: "relative" }}>
                <button className={`btn-ghost${ccActiveCount > 0 || showFilter ? " on-accent" : ""}`} onClick={(e) => { e.stopPropagation(); setShowFilter((v) => !v); }}>
                  <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 5h18l-7 8v5l-4 2v-7z" /></svg>
                    高级筛选{ccActiveCount > 0 ? ` · ${ccActiveCount}` : ""}
                  </span>
                </button>
                {showFilter && <FilterBuilder fields={CARRIER_FILTER_FIELDS} model={model} onChange={setModel} onClose={() => setShowFilter(false)} />}
              </div>
            </>
          }
          toolbarRight={anyFilter ? <button className="linkish small" onClick={() => { setSearch(""); setModel(EMPTY_MODEL); }}>重置</button> : undefined}
        />
      )}
      {openId && <CarrierDrawer carrierId={openId} onClose={() => setOpenId(null)} />}
    </div>
  );
}
