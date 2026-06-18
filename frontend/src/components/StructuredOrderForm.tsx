import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type { Customer, OrderCargoItem, OrderStop, OrderTemplate, Paginated, ParsedOrder } from "../api/types";
import {
  BUSINESS_TYPE_LABEL,
  ORDER_CHANNEL_LABEL,
  PRIORITY_LABEL,
  SETTLEMENT_LABEL,
  SOURCE_TYPE_LABEL,
} from "../api/types";

interface FormState {
  customer: string;
  channel: string;
  source: string;
  source_type: string;
  business_type: string;
  priority: string;
  settlement_type: string;
  origin: string;
  destination: string;
  cargo_value: string;
  package_type: string;
  is_hazardous: boolean;
  temperature_range: string;
  quoted_amount: string;
  expected_pickup_at: string;
  expected_delivery_at: string;
  remark: string;
}

const EMPTY_FORM: FormState = {
  customer: "", channel: "cs", source: "", source_type: "enterprise", business_type: "ftl",
  priority: "normal", settlement_type: "monthly", origin: "", destination: "", cargo_value: "",
  package_type: "", is_hazardous: false, temperature_range: "", quoted_amount: "",
  expected_pickup_at: "", expected_delivery_at: "", remark: "",
};

const emptyCargo = (): OrderCargoItem => ({ name: "", quantity: "", weight_ton: "", volume_cbm: "", package_type: "", temperature_range: "", remark: "" });
const emptyStop = (t: "pickup" | "delivery"): OrderStop => ({ stop_type: t, city: "", address: "", contact_name: "", contact_phone: "", expected_start: "", expected_end: "", cargo_note: "" });

export function StructuredOrderForm({ onCreated }: { onCreated: () => void }) {
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [cargo, setCargo] = useState<OrderCargoItem[]>([emptyCargo()]);
  const [stops, setStops] = useState<OrderStop[]>([emptyStop("pickup"), emptyStop("delivery")]);
  const [paste, setPaste] = useState("");
  const [tplName, setTplName] = useState("");

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setForm((f) => ({ ...f, [k]: v }));

  const customers = useQuery({ queryKey: ["customers"], queryFn: () => apiGet<Paginated<Customer>>("/customers?page_size=500") });
  const templates = useQuery({ queryKey: ["order-templates"], queryFn: () => apiGet<Paginated<OrderTemplate>>("/order-templates?page_size=100") });

  interface AddrItem { city: string; address: string; contact_name: string; contact_phone: string }
  const addressBook = useQuery({
    queryKey: ["customer-addresses", form.customer],
    queryFn: () => apiGet<{ pickup: AddrItem[]; delivery: AddrItem[] }>(`/orders/customer-addresses?customer=${form.customer}`),
    enabled: Boolean(form.customer),
  });

  const fillStopFromBook = (type: "pickup" | "delivery", a: AddrItem) => {
    setStops((prev) => {
      const idx = prev.findIndex((s) => s.stop_type === type);
      const filled = { ...emptyStop(type), city: a.city, address: a.address, contact_name: a.contact_name, contact_phone: a.contact_phone };
      if (idx >= 0) return prev.map((s, j) => (j === idx ? filled : s));
      return [...prev, filled];
    });
    if (type === "pickup" && a.city) set("origin", a.city);
    if (type === "delivery" && a.city) set("destination", a.city);
  };

  const reset = () => {
    setForm(EMPTY_FORM);
    setCargo([emptyCargo()]);
    setStops([emptyStop("pickup"), emptyStop("delivery")]);
    setPaste("");
  };

  const cleanCargo = () => cargo.filter((c) => c.name.trim());
  const cleanStops = () => stops.filter((s) => s.address.trim() || s.city.trim());

  const payload = (status: string) => ({
    channel: form.channel,
    source: form.source,
    status,
    fields: {
      customer: form.customer || undefined,
      source_type: form.source_type, business_type: form.business_type, priority: form.priority,
      settlement_type: form.settlement_type, origin: form.origin, destination: form.destination,
      cargo_value: form.cargo_value || undefined, package_type: form.package_type,
      is_hazardous: form.is_hazardous, temperature_range: form.temperature_range,
      quoted_amount: form.quoted_amount || undefined,
      expected_pickup_at: form.expected_pickup_at || undefined,
      expected_delivery_at: form.expected_delivery_at || undefined,
      remark: form.remark,
    },
    cargo_items: cleanCargo(),
    stops: cleanStops(),
  });

  const submit = useMutation({
    mutationFn: (status: string) => apiPost("/orders/intake", payload(status)),
    onSuccess: (_d, status) => {
      toast.success(status === "draft" ? "已存草稿" : "建单成功（待确认）");
      reset();
      onCreated();
    },
  });

  const aiParse = useMutation({
    mutationFn: () => apiPost<ParsedOrder>("/orders/parse-preview", { text: paste }),
    onSuccess: (d) => {
      const f = d.fields ?? {};
      setForm((prev) => ({
        ...prev,
        origin: String(f.origin ?? prev.origin),
        destination: String(f.destination ?? prev.destination),
      }));
      if (f.cargo_weight_ton || f.cargo_quantity || f.cargo_desc) {
        setCargo([{ ...emptyCargo(), name: String(f.cargo_desc ?? "货物"), quantity: Number(f.cargo_quantity ?? 0) || "", weight_ton: Number(f.cargo_weight_ton ?? 0) || "", volume_cbm: Number(f.cargo_volume_cbm ?? 0) || "" }]);
      }
      if (f.contact_phone) {
        setStops((prev) => prev.map((s, i) => (i === 0 ? { ...s, contact_phone: String(f.contact_phone) } : s)));
      }
      toast.success("已根据消息填充表单，请核对补全");
    },
  });

  const quote = useMutation({
    mutationFn: () => apiPost<{ amount: number; matched: boolean; rule_name: string }>("/orders/quote", {
      customer: form.customer || undefined, origin: form.origin, destination: form.destination,
      cargo_weight_ton: cleanCargo().reduce((s, c) => s + (Number(c.weight_ton) || 0), 0),
    }),
    onSuccess: (d) => {
      if (d.matched) {
        set("quoted_amount", String(d.amount));
        toast.success(`已按「${d.rule_name}」估价 ${fmtMoney(d.amount)}`);
      } else {
        toast.info("未匹配到计价规则，请手工填写报价");
      }
    },
  });

  const saveTpl = useMutation({
    mutationFn: () => apiPost("/order-templates", { name: tplName, payload: payload("draft") }),
    onSuccess: () => { setTplName(""); templates.refetch(); toast.success("模板已保存"); },
  });

  const applyTpl = (id: string) => {
    const tpl = templates.data?.items.find((t) => t.id === id);
    if (!tpl) return;
    const p = tpl.payload as ReturnType<typeof payload>;
    const f = p.fields ?? {};
    setForm({ ...EMPTY_FORM, channel: p.channel ?? "cs", source: p.source ?? "", ...f, customer: String(f.customer ?? ""), cargo_value: String(f.cargo_value ?? ""), quoted_amount: String(f.quoted_amount ?? ""), expected_pickup_at: String(f.expected_pickup_at ?? ""), expected_delivery_at: String(f.expected_delivery_at ?? "") } as FormState);
    if (p.cargo_items?.length) setCargo(p.cargo_items.map((c) => ({ ...emptyCargo(), ...c })));
    if (p.stops?.length) setStops(p.stops.map((s) => ({ ...emptyStop(s.stop_type), ...s })));
    toast.success(`已套用模板「${tpl.name}」`);
  };

  const totalWeight = cleanCargo().reduce((s, c) => s + (Number(c.weight_ton) || 0), 0);
  const totalQty = cleanCargo().reduce((s, c) => s + (Number(c.quantity) || 0), 0);
  const valid = form.origin.trim() && form.destination.trim();

  return (
    <div className="panel">
      <div className="panel-head">
        标准录单
        <span className="ai-pill">企业级 · 多货多站</span>
      </div>

      {/* AI 速录 */}
      <div className="ai-box">
        <input placeholder="AI 速录：粘贴客户消息，自动填充线路/货量/电话…" value={paste} onChange={(e) => setPaste(e.target.value)} />
        <button className="btn-ghost" disabled={!paste.trim() || aiParse.isPending} onClick={() => aiParse.mutate()}>{aiParse.isPending ? "解析中…" : "AI 填充"}</button>
      </div>

      <div className="form-section">
        {templates.data && templates.data.items.length > 0 && (
          <select defaultValue="" onChange={(e) => { if (e.target.value) applyTpl(e.target.value); e.target.value = ""; }}>
            <option value="">套用模板…</option>
            {templates.data.items.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        )}
      </div>

      {/* 客户与商务 */}
      <div className="form-section">
        <div className="section-label">客户与商务</div>
        <div className="grid-form">
          <label>客户
            <select value={form.customer} onChange={(e) => set("customer", e.target.value)}>
              <option value="">选择客户（可选）</option>
              {(customers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </label>
          <label>渠道
            <select value={form.channel} onChange={(e) => set("channel", e.target.value)}>
              {Object.entries(ORDER_CHANNEL_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </label>
          <label>客户类型
            <select value={form.source_type} onChange={(e) => set("source_type", e.target.value)}>
              {Object.entries(SOURCE_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </label>
          <label>业务类型
            <select value={form.business_type} onChange={(e) => set("business_type", e.target.value)}>
              {Object.entries(BUSINESS_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </label>
          <label>优先级
            <select value={form.priority} onChange={(e) => set("priority", e.target.value)}>
              {Object.entries(PRIORITY_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </label>
          <label>结算方式
            <select value={form.settlement_type} onChange={(e) => set("settlement_type", e.target.value)}>
              {Object.entries(SETTLEMENT_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </label>
          <label>来源备注
            <input value={form.source} onChange={(e) => set("source", e.target.value)} placeholder="群名/坐席" />
          </label>
        </div>
      </div>

      {/* 线路与装卸站点 */}
      <div className="form-section">
        <div className="section-label">线路与装卸站点（多提多送）</div>
        <div className="grid-form">
          <label>始发城市 *<input value={form.origin} onChange={(e) => set("origin", e.target.value)} /></label>
          <label>目的城市 *<input value={form.destination} onChange={(e) => set("destination", e.target.value)} /></label>
        </div>
        {form.customer && ((addressBook.data?.pickup.length ?? 0) > 0 || (addressBook.data?.delivery.length ?? 0) > 0) && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, margin: "8px 0" }}>
            <span className="muted small">📒 地址簿：</span>
            {(addressBook.data?.pickup ?? []).map((a, i) => (
              <button key={`p${i}`} className="chip" onClick={() => fillStopFromBook("pickup", a)}>提·{a.city}{a.address ? ` ${a.address.slice(0, 8)}` : ""}</button>
            ))}
            {(addressBook.data?.delivery ?? []).map((a, i) => (
              <button key={`d${i}`} className="chip" onClick={() => fillStopFromBook("delivery", a)}>送·{a.city}{a.address ? ` ${a.address.slice(0, 8)}` : ""}</button>
            ))}
          </div>
        )}
        {stops.map((s, i) => (
          <div key={i} className="line-row">
            <select value={s.stop_type} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, stop_type: e.target.value as "pickup" | "delivery" } : x))}>
              <option value="pickup">提货</option>
              <option value="delivery">送货</option>
            </select>
            <input placeholder="城市" style={{ width: 90 }} value={s.city} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, city: e.target.value } : x))} />
            <input placeholder="详细地址" style={{ flex: 2 }} value={s.address} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, address: e.target.value } : x))} />
            <input placeholder="联系人" style={{ width: 90 }} value={s.contact_name} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, contact_name: e.target.value } : x))} />
            <input placeholder="电话" style={{ width: 130 }} value={s.contact_phone} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, contact_phone: e.target.value } : x))} />
            <button className="btn-ghost" onClick={() => setStops((p) => p.filter((_, j) => j !== i))} disabled={stops.length <= 1}>×</button>
          </div>
        ))}
        <button className="btn-ghost" onClick={() => setStops((p) => [...p, emptyStop("delivery")])}>+ 增加站点</button>
      </div>

      {/* 货物明细 */}
      <div className="form-section">
        <div className="section-label">货物明细（一单多货）· 合计 {totalQty} 件 / {totalWeight.toFixed(2)} 吨</div>
        {cargo.map((c, i) => (
          <div key={i} className="line-row">
            <input placeholder="品名" style={{ flex: 2 }} value={c.name} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, name: e.target.value } : x))} />
            <input placeholder="件数" style={{ width: 70 }} value={c.quantity} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, quantity: e.target.value } : x))} />
            <input placeholder="吨" style={{ width: 70 }} value={c.weight_ton} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, weight_ton: e.target.value } : x))} />
            <input placeholder="方" style={{ width: 70 }} value={c.volume_cbm} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, volume_cbm: e.target.value } : x))} />
            <input placeholder="包装" style={{ width: 80 }} value={c.package_type} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, package_type: e.target.value } : x))} />
            <input placeholder="温区" style={{ width: 90 }} value={c.temperature_range} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, temperature_range: e.target.value } : x))} />
            <button className="btn-ghost" onClick={() => setCargo((p) => p.filter((_, j) => j !== i))} disabled={cargo.length <= 1}>×</button>
          </div>
        ))}
        <button className="btn-ghost" onClick={() => setCargo((p) => [...p, emptyCargo()])}>+ 增加货物</button>
      </div>

      {/* 时效与商务条款 */}
      <div className="form-section">
        <div className="section-label">时效 · 报价 · 要求</div>
        <div className="grid-form">
          <label>要求提货时间<input type="datetime-local" value={form.expected_pickup_at} onChange={(e) => set("expected_pickup_at", e.target.value)} /></label>
          <label>要求送达时间<input type="datetime-local" value={form.expected_delivery_at} onChange={(e) => set("expected_delivery_at", e.target.value)} /></label>
          <label>报价(元)
            <div style={{ display: "flex", gap: 6 }}>
              <input value={form.quoted_amount} onChange={(e) => set("quoted_amount", e.target.value)} />
              <button className="btn-ghost" disabled={quote.isPending} onClick={() => quote.mutate()}>⚡自动</button>
            </div>
          </label>
          <label>货值(元)<input value={form.cargo_value} onChange={(e) => set("cargo_value", e.target.value)} /></label>
          <label>包装方式<input value={form.package_type} onChange={(e) => set("package_type", e.target.value)} /></label>
          <label>温区<input value={form.temperature_range} onChange={(e) => set("temperature_range", e.target.value)} placeholder="如 -18~0" /></label>
          <label className="check-label"><input type="checkbox" checked={form.is_hazardous} onChange={(e) => set("is_hazardous", e.target.checked)} /> 危险品</label>
        </div>
        <textarea className="search" style={{ width: "100%", minHeight: 56, marginTop: 8 }} placeholder="备注 / 特殊要求" value={form.remark} onChange={(e) => set("remark", e.target.value)} />
      </div>

      {/* 操作 */}
      <div className="form-actions">
        <button className="btn-primary" disabled={!valid || submit.isPending} onClick={() => submit.mutate("pending_confirm")}>建单（待确认）</button>
        <button className="btn-ghost" disabled={submit.isPending} onClick={() => submit.mutate("draft")}>存草稿</button>
        <button className="btn-ghost" onClick={reset}>清空</button>
        <span style={{ flex: 1 }} />
        <input placeholder="模板名" style={{ width: 140 }} value={tplName} onChange={(e) => setTplName(e.target.value)} />
        <button className="btn-ghost" disabled={!tplName.trim() || saveTpl.isPending} onClick={() => saveTpl.mutate()}>存为模板</button>
      </div>
      {!valid && <div className="muted small" style={{ padding: "0 18px 14px" }}>请至少填写始发与目的城市</div>}
    </div>
  );
}
