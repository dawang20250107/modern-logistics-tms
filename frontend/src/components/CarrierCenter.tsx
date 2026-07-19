import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiGet } from "../api/client";
import type { Carrier, CarrierLanePrice, Paginated } from "../api/types";

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
            <div className="muted" style={{ padding: 16 }}>加载中…</div>
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
                  <div className="muted small">加载中…</div>
                ) : (lanes.data?.items ?? []).length === 0 ? (
                  <div className="muted small">尚未维护该承运商的线路价库。</div>
                ) : (
                  <table className="table" style={{ fontSize: 12.5 }}>
                    <thead><tr><th>线路</th><th>车型</th><th className="num">标准价</th><th className="num">最近成交</th><th>标记</th></tr></thead>
                    <tbody>
                      {(lanes.data?.items ?? []).map((l) => (
                        <tr key={l.id}>
                          <td>{l.origin_city}→{l.dest_city}</td>
                          <td className="small">{l.vehicle_type || "—"}{l.vehicle_length_m ? ` ${l.vehicle_length_m}m` : ""}</td>
                          <td className="num">¥{Number(l.standard_price).toLocaleString()}</td>
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
  const [kw, setKw] = useState("");
  const [openId, setOpenId] = useState<string | null>(null);
  const q = useQuery({ queryKey: ["cc-carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=300") });

  const rows = useMemo(() => {
    const items = q.data?.items ?? [];
    const k = kw.trim().toLowerCase();
    return k ? items.filter((c) => `${c.code} ${c.name} ${c.contact_phone ?? ""} ${c.city ?? ""}`.toLowerCase().includes(k)) : items;
  }, [q.data, kw]);

  return (
    <div className="panel">
      <div className="panel-head">
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>承运商清单<span className="ai-pill">{rows.length}</span></span>
        <input className="search" style={{ width: 260 }} placeholder="搜索承运商 / 城市 / 电话" value={kw} onChange={(e) => setKw(e.target.value)} />
      </div>
      {q.isLoading ? (
        <div className="muted" style={{ padding: 16 }}>加载中…</div>
      ) : rows.length === 0 ? (
        <div className="muted" style={{ padding: 16 }}>暂无承运商，请先在承运商中心建档。</div>
      ) : (
        <table className="table">
          <thead><tr>
            <th>承运商</th><th>类型</th><th>城市</th><th>评级/风控</th><th className="num">账期</th><th>到期预警</th><th>状态</th>
          </tr></thead>
          <tbody>
            {rows.map((c) => (
              <tr key={c.id} style={{ cursor: "pointer" }} onClick={() => setOpenId(c.id)}>
                <td><span className="link">{c.name}</span> <span className="muted small mono">{c.code}</span></td>
                <td className="small">{c.carrier_type_label || "—"}</td>
                <td className="small">{c.city || "—"}</td>
                <td>{riskTag(c)}</td>
                <td className="num">{c.credit_days ?? "—"}天</td>
                <td>
                  {c.expiry_alerts && c.expiry_alerts.length > 0
                    ? <span className={`tag tag-${c.expiry_alerts.some((a) => a.expired) ? "high" : "medium"}`}>{c.expiry_alerts.length} 项临期</span>
                    : <span className="muted small">—</span>}
                </td>
                <td><span className={`tag ${c.is_active ? "tag-low" : "tag-none"}`}>{c.is_active ? "启用" : "停用"}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {openId && <CarrierDrawer carrierId={openId} onClose={() => setOpenId(null)} />}
    </div>
  );
}
