import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiDelete, apiGet, apiPatch, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { fmtMoney, fmtNum } from "../api/format";
import { toast } from "../api/toast";
import type { Carrier, Customer, Paginated, PricingRule } from "../api/types";
import { PRICE_TYPE_LABEL } from "../api/types";
import { EmptyState } from "../components/EmptyState";

const CHARGE_METHOD_LABEL: Record<string, string> = {
  tiered_weight: "按重量阶梯", flat: "整车一口价", per_volume: "按方计费",
  per_piece: "按件计费", per_km: "按公里计费", per_ton_km: "吨公里计费",
};

interface RuleForm {
  name: string;
  price_type: "income" | "cost";
  charge_method: string;
  expense_item_code: string;
  customer: string;
  carrier: string;
  route_name: string;
  base_price: string;
  unit_price: string;
  min_charge_qty: string;
  min_price: string;
  tier_prices: Array<{ min_ton: number; max_ton: number; price: number }>;
  volumetric_factor: string;
  fuel_surcharge_pct: string;
  priority: string;
  is_active: boolean;
}

const EMPTY: RuleForm = {
  name: "", price_type: "income", charge_method: "tiered_weight", expense_item_code: "FREIGHT", customer: "", carrier: "",
  route_name: "", base_price: "0", unit_price: "0", min_charge_qty: "0", min_price: "0",
  tier_prices: [], volumetric_factor: "0.33", fuel_surcharge_pct: "0", priority: "0", is_active: true,
};

export function PricingPage() {
  const queryClient = useQueryClient();
  const [typeFilter, setTypeFilter] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState<RuleForm>(EMPTY);
  const set = <K extends keyof RuleForm>(k: K, v: RuleForm[K]) => setForm((f) => ({ ...f, [k]: v }));

  const rules = useQuery({
    queryKey: ["pricing-rules", typeFilter],
    queryFn: () => apiGet<Paginated<PricingRule>>(`/finance/pricing-rules?page_size=200${typeFilter ? `&price_type=${typeFilter}` : ""}`),
  });
  const customers = useQuery({ queryKey: ["customers"], queryFn: () => apiGet<Paginated<Customer>>("/customers?page_size=500") });
  const carriers = useQuery({ queryKey: ["carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=500") });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["pricing-rules"] });

  const payload = () => ({
    name: form.name, price_type: form.price_type, charge_method: form.charge_method,
    expense_item_code: form.expense_item_code || "FREIGHT",
    customer: form.customer || null, carrier: form.carrier || null, route_name: form.route_name,
    base_price: form.base_price || 0, unit_price: form.unit_price || 0,
    min_charge_qty: form.min_charge_qty || 0, min_price: form.min_price || 0,
    tier_prices: form.tier_prices, volumetric_factor: form.volumetric_factor || 0.3333,
    fuel_surcharge_pct: form.fuel_surcharge_pct || 0,
    priority: Number(form.priority) || 0, is_active: form.is_active,
  });

  const reset = () => { setEditing(null); setForm(EMPTY); };
  const save = useMutation({
    mutationFn: () => editing ? apiPatch(`/finance/pricing-rules/${editing}`, payload()) : apiPost("/finance/pricing-rules", payload()),
    onSuccess: () => { toast.success(editing ? "已更新合同价" : "已新增合同价"); reset(); invalidate(); },
  });
  const patch = useMutation({
    mutationFn: (v: { id: string; is_active: boolean }) => apiPatch(`/finance/pricing-rules/${v.id}`, { is_active: v.is_active }),
    onSuccess: invalidate,
    meta: { silent: true },
  });
  const remove = useMutation({
    mutationFn: (id: string) => apiDelete(`/finance/pricing-rules/${id}`),
    onSuccess: () => { toast.success("已删除"); invalidate(); },
  });

  const startEdit = (r: PricingRule) => {
    setEditing(r.id);
    setForm({
      name: r.name, price_type: r.price_type, charge_method: r.charge_method ?? "tiered_weight",
      expense_item_code: r.expense_item_code,
      customer: r.customer ?? "", carrier: r.carrier ?? "", route_name: r.route_name,
      base_price: r.base_price, unit_price: r.unit_price ?? "0", min_charge_qty: r.min_charge_qty ?? "0",
      min_price: r.min_price, tier_prices: r.tier_prices || [],
      volumetric_factor: r.volumetric_factor, fuel_surcharge_pct: r.fuel_surcharge_pct,
      priority: String(r.priority), is_active: r.is_active,
    });
  };

  const items = rules.data?.items ?? [];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          {editing ? "编辑合同价 / 计价规则" : "新增合同价 / 计价规则"}
          
        </div>
        <div className="form-section" style={{ borderBottom: "none" }}>
          <div className="grid-form">
            <label>规则名称 *<input value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="如：比亚迪-沪蓉整车" /></label>
            <label>价格类型
              <select value={form.price_type} onChange={(e) => set("price_type", e.target.value as "income" | "cost")}>
                {Object.entries(PRICE_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
            <label>适用客户（空=通用）
              <select value={form.customer} onChange={(e) => set("customer", e.target.value)}>
                <option value="">全部客户</option>
                {(customers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </label>
            <label>适用承运商（空=通用）
              <select value={form.carrier} onChange={(e) => set("carrier", e.target.value)}>
                <option value="">全部承运商</option>
                {(carriers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </label>
            <label>适用线路（空=通用）<input value={form.route_name} onChange={(e) => set("route_name", e.target.value)} placeholder="上海→成都" /></label>
            <label>计费方式
              <select value={form.charge_method} onChange={(e) => set("charge_method", e.target.value)}>
                {Object.entries(CHARGE_METHOD_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
            <label>{form.charge_method === "flat" ? "整车固定价(元)" : "起步价(元)"}<input value={form.base_price} onChange={(e) => set("base_price", e.target.value)} /></label>
            {form.charge_method !== "flat" && form.charge_method !== "tiered_weight" && (
              <label>单价({form.charge_method === "per_volume" ? "元/方" : form.charge_method === "per_piece" ? "元/件" : form.charge_method === "per_km" ? "元/公里" : "元/吨公里"})
                <input value={form.unit_price} onChange={(e) => set("unit_price", e.target.value)} />
              </label>
            )}
            {(form.charge_method === "per_volume" || form.charge_method === "per_piece" || form.charge_method === "per_ton_km" || form.charge_method === "tiered_weight") && (
              <label>最低计费量({form.charge_method === "per_volume" ? "方" : form.charge_method === "per_piece" ? "件" : "吨"})
                <input value={form.min_charge_qty} onChange={(e) => set("min_charge_qty", e.target.value)} />
              </label>
            )}
            <label>最低价(元下限)<input value={form.min_price} onChange={(e) => set("min_price", e.target.value)} /></label>
            <label>燃油附加率(如0.025)<input value={form.fuel_surcharge_pct} onChange={(e) => set("fuel_surcharge_pct", e.target.value)} /></label>
            <label>优先级（大者优先）<input value={form.priority} onChange={(e) => set("priority", e.target.value)} /></label>
            <label className="check-label"><input type="checkbox" checked={form.is_active} onChange={(e) => set("is_active", e.target.checked)} /> 启用</label>
          </div>
          <div className="muted small" style={{ marginTop: 8 }}>
            六种计费方式：整车一口价 / 按重量阶梯 / 按方 / 按件 / 按公里 / 吨公里；均取「最低价」为金额下限并叠加燃油附加。录单"自动报价"按客户/线路匹配优先级最高的收入价规则。
          </div>
        </div>
        <div className="form-actions">
          <button className="btn-primary" disabled={!form.name.trim() || save.isPending} onClick={() => save.mutate()}>
            {editing ? "保存修改" : "新增规则"}
          </button>
          {editing && <button className="btn-ghost" onClick={reset}>取消编辑</button>}
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">合同价目录 · {rules.data?.total ?? 0}</div>
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8 }}>
          <button className={`chip${typeFilter === "" ? " chip-on" : ""}`} onClick={() => setTypeFilter("")}>全部</button>
          <button className={`chip${typeFilter === "income" ? " chip-on" : ""}`} onClick={() => setTypeFilter("income")}>收入价</button>
          <button className={`chip${typeFilter === "cost" ? " chip-on" : ""}`} onClick={() => setTypeFilter("cost")}>支出价</button>
        </div>
        {rules.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : items.length === 0 ? (
          <EmptyState title="暂无合同价规则" hint="新增规则后，录单即可自动报价" />
        ) : (
          <table className="table">
            <thead>
              <tr><th>合同规则名称</th><th>方向</th><th>计费方式</th><th>定向客户</th><th>定向承运商</th><th>线路路由</th><th>起步/固定价</th><th>阶梯价层数</th><th>重抛比</th><th>燃油金</th><th>启用</th><th>操作</th></tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id} style={editing === r.id ? { background: "var(--brand-light)" } : {}}>
                  <td style={{ fontWeight: "bold" }}>{r.name}</td>
                  <td><span className={`tag tag-${r.price_type === "income" ? "low" : "medium"}`}>{r.price_type === "income" ? "应收" : "应付"}</span></td>
                  <td><span className="tag" style={{ background: "rgba(37,99,235,0.08)", color: "var(--brand)" }}>{CHARGE_METHOD_LABEL[r.charge_method] ?? r.charge_method}</span></td>
                  <td>{r.customer_name || "全局通用"}</td>
                  <td>{r.carrier_name || "全局通用"}</td>
                  <td>{r.route_name || "全局通用"}</td>
                  <td className="mono" style={{ color: "var(--brand)", fontWeight: "bold" }}>{fmtMoney(r.base_price)}</td>
                  <td>{r.tier_prices && r.tier_prices.length > 0 ? <span className="tag" style={{ background: "rgba(0,0,0,0.05)" }}>{r.tier_prices.length} 级</span> : "—"}</td>
                  <td>{r.volumetric_factor}</td>
                  <td>{Number(r.fuel_surcharge_pct) > 0 ? <span className="tag tag-high">+{fmtNum(Number(r.fuel_surcharge_pct) * 100, 1)}%</span> : "—"}</td>
                  <td>
                    <label className="switch-mini">
                      <input type="checkbox" checked={r.is_active} onChange={() => patch.mutate({ id: r.id, is_active: !r.is_active })} />
                      <span className={`tag tag-${r.is_active ? "low" : "none"}`}>{r.is_active ? "启用" : "停用"}</span>
                    </label>
                  </td>
                  <td className="row-actions">
                    <button className="btn-ghost" onClick={() => startEdit(r)}>编辑</button>
                    <button className="btn-ghost" disabled={remove.isPending} onClick={async () => {
                      if (await confirmAction({ message: `删除规则「${r.name}」？`, tone: "danger", confirmText: "删除" })) remove.mutate(r.id);
                    }}>删除</button>
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
