import { useMutation, useQuery } from "@tanstack/react-query";
import { useRef, useState } from "react";

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
import { IconSparkles, IconSave, IconPlus, IconX, IconCheck, IconZap } from "./Icons";
import { CityCombobox } from "./CityCombobox";
import { RegionCascader } from "./RegionCascader";
import { DateTimeField } from "./DateTimeField";

interface FormState {
  customer: string;
  channel: string;
  source: string;
  source_type: string;
  business_type: string;
  priority: string;
  settlement_type: string;
  freight_term: string;
  freight_payer: string;
  cod_amount: string;
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
  priority: "normal", settlement_type: "monthly", freight_term: "prepaid", freight_payer: "shipper",
  cod_amount: "", origin: "", destination: "", cargo_value: "",
  package_type: "", is_hazardous: false, temperature_range: "", quoted_amount: "",
  expected_pickup_at: "", expected_delivery_at: "", remark: "",
};

interface B2BPartner {
  id: string;
  partner_type: "shipper" | "consignee" | "supplier";
  partner_type_label: string;
  code: string;
  name: string;
  contact_name: string;
  contact_phone: string;
  address: string;
  city: string;
  is_active: boolean;
}

const CHANNEL_META: Record<string, { icon: string; hint: string; sourcePlaceholder: string }> = {
  cs: { icon: "🎧", hint: "客服代客户录入订单。", sourcePlaceholder: "坐席/工号" },
  self: { icon: "🧑‍💼", hint: "客户自助提交的订单，请核对后确认。", sourcePlaceholder: "客户账号" },
  miniprogram: { icon: "📱", hint: "小程序下单，核对联系人与地址后确认。", sourcePlaceholder: "小程序用户" },
  wechat_group: { icon: "", hint: "粘贴微信群消息到上方解析框自动填充。", sourcePlaceholder: "群名称" },
  api: { icon: "🔌", hint: "API/EDI 对接，系统自动接单，可在此补录。", sourcePlaceholder: "对接系统" },
};

const emptyCargo = (): OrderCargoItem => ({ name: "", quantity: "", weight_ton: "", volume_cbm: "", package_type: "", temperature_range: "", remark: "" });
// 本地站点表单：在 OrderStop 基础上带省/区，供三级级联选址（提交时合并进 city/address）
type StopForm = OrderStop & { province?: string; district?: string };
const emptyStop = (t: "pickup" | "delivery"): StopForm => ({ stop_type: t, city: "", address: "", contact_name: "", contact_phone: "", expected_start: "", expected_end: "", cargo_note: "", province: "", district: "" });

export function StructuredOrderForm({ onCreated, onCustomerChange }: { onCreated: () => void; onCustomerChange?: (id: string) => void }) {
  const [activeMode, setActiveMode] = useState<"standard" | "ai" | "batch">("standard");
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [cargo, setCargo] = useState<OrderCargoItem[]>([emptyCargo()]);
  const [stops, setStops] = useState<StopForm[]>([emptyStop("pickup"), emptyStop("delivery")]);
  const [paste, setPaste] = useState("");
  const [tplName, setTplName] = useState("");
  const [bulkText, setBulkText] = useState("");
  const [continuous, setContinuous] = useState(false);

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setForm((f) => ({ ...f, [k]: v }));

  const customers = useQuery({ queryKey: ["customers"], queryFn: () => apiGet<Paginated<Customer>>("/customers?page_size=500") });
  const templates = useQuery({ queryKey: ["order-templates"], queryFn: () => apiGet<Paginated<OrderTemplate>>("/order-templates?page_size=100") });

  // === 查询 B2B 上下游业务伙伴 ===
  const b2bPartners = useQuery({
    queryKey: ["b2b-partners"],
    queryFn: () => apiGet<Paginated<B2BPartner>>("/b2b-partners?page_size=500"),
  });

  const shippers = b2bPartners.data?.items.filter((p) => p.partner_type === "shipper" && p.is_active) ?? [];
  const consignees = b2bPartners.data?.items.filter((p) => p.partner_type === "consignee" && p.is_active) ?? [];

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

  const handlePartnerSelect = (type: "pickup" | "delivery", partnerId: string) => {
    const list = type === "pickup" ? shippers : consignees;
    const partner = list.find((p) => p.id === partnerId);
    if (!partner) return;

    fillStopFromBook(type, {
      city: partner.city,
      address: partner.address,
      contact_name: partner.contact_name,
      contact_phone: partner.contact_phone,
    });
    toast.success(`已填充${type === "pickup" ? "发货方" : "收货方"}：${partner.name}`);
  };

  const reset = () => {
    setForm(EMPTY_FORM);
    setCargo([emptyCargo()]);
    setStops([emptyStop("pickup"), emptyStop("delivery")]);
    setPaste("");
    setBulkText("");
  };

  // 连续建单：保留客户/来源/业务类型/结算等"抬头"，仅清空货物/线路/时间，快速录下一单
  const resetKeep = () => {
    setForm((f) => ({
      ...EMPTY_FORM,
      channel: f.channel, source: f.source, source_type: f.source_type,
      business_type: f.business_type, priority: f.priority, settlement_type: f.settlement_type,
      freight_term: f.freight_term, freight_payer: f.freight_payer, customer: f.customer,
    }));
    setCargo([emptyCargo()]);
    setStops([emptyStop("pickup"), emptyStop("delivery")]);
    setPaste("");
  };

  const cleanCargo = () => cargo.filter((c) => c.name.trim());
  const cleanStops = () => stops
    .filter((s) => s.address.trim() || s.city.trim())
    .map((s) => {
      const region = [s.province, s.district].filter((x) => x && x !== "市辖区").join(" ");
      const address = region && !s.address.startsWith(region) ? `${region} ${s.address}`.trim() : s.address;
      return { stop_type: s.stop_type, city: s.city, address, contact_name: s.contact_name, contact_phone: s.contact_phone, expected_start: s.expected_start, expected_end: s.expected_end, cargo_note: s.cargo_note };
    });

  const payload = (status: string) => ({
    channel: form.channel,
    source: form.source,
    status,
    fields: {
      customer: form.customer || undefined,
      source_type: form.source_type, business_type: form.business_type, priority: form.priority,
      settlement_type: form.settlement_type,
      freight_term: form.freight_term, freight_payer: form.freight_payer,
      cod_amount: form.cod_amount || undefined,
      origin: form.origin, destination: form.destination,
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
      toast.success(status === "draft" ? "已存草稿" : continuous ? "建单成功，可继续录入" : "建单成功");
      if (continuous && status !== "draft") resetKeep();
      else reset();
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
      toast.success("解析完成，请核对");
    },
  });

  const quote = useMutation({
    mutationFn: () => apiPost<{ amount: number; matched: boolean; rule_name: string; chargeable_weight: number; by_volume: boolean }>("/orders/quote", {
      customer: form.customer || undefined, origin: form.origin, destination: form.destination,
      cargo_weight_ton: cleanCargo().reduce((s, c) => s + (Number(c.weight_ton) || 0), 0),
      cargo_volume_cbm: cleanCargo().reduce((s, c) => s + (Number(c.volume_cbm) || 0), 0),
    }),
    onSuccess: (d) => {
      const cw = d.by_volume ? `（按抛重 ${d.chargeable_weight} 吨）` : "";
      if (d.matched) {
        set("quoted_amount", String(d.amount));
        toast.success(`已按「${d.rule_name}」计价 ${fmtMoney(d.amount)}${cw}`);
      } else {
        toast.info(`未匹配到计价规则，请手工填写报价${cw}`);
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

  // === 批量 CSV / TEXT 录单解析 ===
  const parseBulkLines = () => {
    return bulkText.split("\n").map((l) => l.trim()).filter(Boolean).map((line, idx) => {
      const parts = line.split(/[,，\t]/).map((s) => s.trim());
      const [origin, destination, weight, qty, phone] = parts;
      return {
        id: idx,
        origin: origin || "—",
        destination: destination || "—",
        weight: weight ? Number(weight) : 0,
        qty: qty ? Number(qty) : 0,
        phone: phone || "—",
        valid: Boolean(origin && destination),
      };
    });
  };

  const bulkRows = parseBulkLines();
  const validBulkRows = bulkRows.filter((r) => r.valid);
  const invalidBulkCount = bulkRows.length - validBulkRows.length;
  // 剔除无效行：仅保留"始发+目的"齐全的行，回写文本框，让用户能一键清障后再导
  const dropInvalidBulk = () => {
    const kept = bulkText.split("\n").filter((l) => {
      const p = l.trim().split(/[,，\t]/).map((s) => s.trim());
      return l.trim() && p[0] && p[1];
    });
    setBulkText(kept.join("\n"));
    toast.success(`已剔除 ${invalidBulkCount} 条无效行`);
  };
  const bulkImportMut = useMutation({
    mutationFn: () => {
      // 只导有效行，缺路线的行跳过（不再让一个坏行卡住整批）
      const rows = validBulkRows.map((r) => ({
        origin: r.origin === "—" ? undefined : r.origin,
        destination: r.destination === "—" ? undefined : r.destination,
        cargo_weight_ton: r.weight || undefined,
        cargo_quantity: r.qty || undefined,
        contact_phone: r.phone === "—" ? undefined : r.phone,
      }));
      return apiPost<{ ok_count: number; failed_count: number }>("/orders/import", { rows });
    },
    onSuccess: (r) => {
      setBulkText("");
      toast.success(`导入完成：${r.ok_count} 单成功，${r.failed_count} 单失败`);
      onCreated();
      setActiveMode("standard");
    },
  });

  const totalWeight = cleanCargo().reduce((s, c) => s + (Number(c.weight_ton) || 0), 0);
  const totalVolume = cleanCargo().reduce((s, c) => s + (Number(c.volume_cbm) || 0), 0);
  const totalQty = cleanCargo().reduce((s, c) => s + (Number(c.quantity) || 0), 0);
  const volumetric = totalVolume * 0.333;
  const chargeable = Math.max(totalWeight, volumetric);
  // 即时校验：按 UI 的「*」必填标记（始发城市 / 目的城市 / 至少一条货品名称）
  const errs = {
    origin: !form.origin.trim(),
    destination: !form.destination.trim(),
    cargo: !cargo.some((c) => c.name.trim()),
  };
  const valid = !errs.origin && !errs.destination && !errs.cargo;
  const [showErrors, setShowErrors] = useState(false);
  const originRef = useRef<HTMLLabelElement>(null);
  const destRef = useRef<HTMLLabelElement>(null);
  const cargoRef = useRef<HTMLDivElement>(null);

  // 提交前校验并「错误定位」：滚动+聚焦到第一个缺失项，并列出缺什么
  const trySubmit = (status: "pending_confirm" | "draft") => {
    if (submit.isPending) return;
    if (status === "draft") { submit.mutate("draft"); return; } // 暂存草稿允许不完整
    if (!valid) {
      setShowErrors(true);
      const missing = [errs.origin && "始发城市", errs.destination && "目的城市", errs.cargo && "货品名称"].filter(Boolean) as string[];
      toast.error(`请补全必填项：${missing.join("、")}`);
      const target = errs.origin ? originRef.current : errs.destination ? destRef.current : cargoRef.current;
      target?.scrollIntoView({ behavior: "smooth", block: "center" });
      target?.querySelector<HTMLElement>("input")?.focus();
      return;
    }
    submit.mutate("pending_confirm");
  };

  return (
    <div
      className="panel"
      style={{ borderRadius: "var(--radius)", border: "1px solid var(--line)" }}
      onKeyDown={(e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === "Enter" && activeMode === "standard" && !submit.isPending) {
          e.preventDefault();
          trySubmit("pending_confirm");
        }
      }}
    >
      {/* 标题 & 药丸标签 */}
      <div className="panel-head" style={{ display: "flex", justifyContent: "space-between", alignItems: "center", borderBottom: "1px solid var(--line)" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ fontSize: 18 }}>新建订单</span>
        </div>
        {templates.data && templates.data.items.length > 0 && (
          <select style={{ width: 140, padding: "4px 8px" }} defaultValue="" onChange={(e) => { if (e.target.value) applyTpl(e.target.value); e.target.value = ""; }}>
            <option value="">快速套用模板…</option>
            {templates.data.items.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        )}
      </div>

      {/* 录入模式选择 */}
      <div style={{ padding: "12px 18px", borderBottom: "1px solid var(--line)", display: "flex", alignItems: "center", gap: 14, flexWrap: "wrap" }}>
        <div className="seg-tabs">
          <button className={activeMode === "standard" ? "active" : ""} onClick={() => setActiveMode("standard")}>标准录单</button>
          <button className={activeMode === "ai" ? "active" : ""} onClick={() => setActiveMode("ai")}>文本解析</button>
          <button className={activeMode === "batch" ? "active" : ""} onClick={() => setActiveMode("batch")}>批量导入</button>
        </div>
        <span className="muted small">
          {activeMode === "standard" ? "逐项精准录入 B2B 细案订单"
            : activeMode === "ai" ? "粘贴微信群 / 邮件消息，自动解析为结构化订单"
            : "多行文本 / Excel 复制批量录单"}
        </span>
      </div>

      {/* === 模式一：AI 协同智能速录 === */}
      {activeMode === "ai" && (
        <div style={{ padding: "18px 18px 24px", display: "grid", gridTemplateColumns: "1.1fr 1.3fr", gap: 18, minHeight: 300 }}>
          {/* 左侧：输入框 */}
          <div className="stack" style={{ gap: 10 }}>
            <div className="section-label" style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <span>粘贴微信消息 / EDI 报文</span>
            </div>
            <textarea
              className="search"
              style={{ width: "100%", height: 220, resize: "none", fontSize: 14, lineHeight: 1.6, padding: 16, borderRadius: 8, background: "var(--panel-3)", border: "1px dashed var(--line-strong)" }}
              placeholder="请粘贴微信群、邮件中的非结构化发货指令：&#10;&#10;例如：“李总，明天下午2点去苏州工业园区星湖街提货，大概5吨的医疗器械，要求冷链2-8度，送到北京海淀医院，收货人王医生 13800138000。这单加急！”"
              value={paste}
              onChange={(e) => setPaste(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                  if (paste.trim()) aiParse.mutate();
                }
              }}
            />
            <div style={{ display: "flex", gap: 10 }}>
              <button
                className="btn-primary"
                style={{ flex: 1, padding: "12px", fontSize: 14, display: "flex", alignItems: "center", justifyContent: "center", gap: 8 }}
                disabled={!paste.trim() || aiParse.isPending}
                onClick={() => aiParse.mutate()}
              >
                {aiParse.isPending ? <><IconSparkles size={16} className="icon-offset"/> 解析中…</> : <><IconZap size={16} className="icon-offset"/> 解析 (Ctrl+Enter)</>}
              </button>
              <button className="btn-ghost" onClick={() => setPaste("")} disabled={!paste} style={{ display: "flex", alignItems: "center", gap: 6 }}><IconX size={14} className="icon-offset" /> 清空</button>
            </div>
          </div>

          {/* 右侧：解析预览 */}
          <div className="stack" style={{ gap: 10, background: "var(--panel-2)", padding: "18px 20px", borderRadius: 10, border: "1px solid var(--line)" }}>
            <div className="section-label" style={{ color: "var(--brand)", fontSize: 14, display: "flex", alignItems: "center", gap: 6 }}><IconSparkles size={16} className="icon-offset" /> 解析结果</div>
            
            {aiParse.isPending ? (
              <div className="stack" style={{ gap: 16, marginTop: 10 }}>
                <div className="skeleton" style={{ height: 32, width: "60%" }}></div>
                <div className="skeleton" style={{ height: 80, width: "100%" }}></div>
                <div className="skeleton" style={{ height: 100, width: "100%" }}></div>
              </div>
            ) : (
              <>
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px 16px", fontSize: 13, background: "var(--panel)", padding: 16, borderRadius: 8, border: "1px solid var(--line)" }}>
                  <div><span className="muted">始发城市：</span><strong style={{ fontSize: 15, color: "var(--brand)" }}>{form.origin || "—"}</strong></div>
                  <div><span className="muted">目的城市：</span><strong style={{ fontSize: 15, color: "var(--brand)" }}>{form.destination || "—"}</strong></div>
                  <div style={{ gridColumn: "1 / -1", height: 1, background: "var(--line)", margin: "4px 0" }}></div>
                  <div><span className="muted">预估货物：</span><strong>{cargo[0]?.name || "—"}</strong></div>
                  <div><span className="muted">预估件数：</span><strong>{cargo[0]?.quantity ? `${cargo[0].quantity} 件` : "—"}</strong></div>
                  <div><span className="muted">预估吨位：</span><strong>{cargo[0]?.weight_ton ? `${cargo[0].weight_ton} 吨` : "—"}</strong></div>
                  <div><span className="muted">预估方数：</span><strong>{cargo[0]?.volume_cbm ? `${cargo[0].volume_cbm} 方` : "—"}</strong></div>
                  <div style={{ gridColumn: "1 / -1", height: 1, background: "var(--line)", margin: "4px 0" }}></div>
                  <div><span className="muted">发货联系：</span><strong>{stops[0]?.contact_phone || "—"}</strong></div>
                  <div><span className="muted">收货联系：</span><strong>{stops[1]?.contact_phone || "—"}</strong></div>
                </div>
                
                <div style={{ marginTop: "auto", display: "flex", flexDirection: "column", gap: 10 }}>
                  <div className="muted small">
                    解析结果已填入标准录单，可直接建单，或进入标准表单补全货物明细与提送站点。
                  </div>
                  <div style={{ display: "flex", gap: 12, marginTop: 4 }}>
                    <button className="btn-secondary" style={{ flex: 1, padding: 12 }} onClick={() => setActiveMode("standard")}>
                      进入标准表单进行微调
                    </button>
                    <button className="btn-primary" style={{ flex: 1.5, padding: 12, background: "var(--green)", borderColor: "var(--green)", display: "flex", alignItems: "center", justifyContent: "center", gap: 8 }} disabled={submit.isPending} onClick={() => trySubmit("pending_confirm")}>
                      <IconCheck size={16} className="icon-offset" /> 确认建单
                    </button>
                  </div>
                </div>
              </>
            )}
          </div>
        </div>
      )}

      {/* === 模式二：多行 / Excel 批量录入 === */}
      {activeMode === "batch" && (
        <div style={{ padding: "18px 18px 24px", display: "flex", flexDirection: "column", gap: 16 }}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 18 }}>
            <div className="stack" style={{ gap: 10 }}>
              <div className="section-label">粘贴多行文本（可从 Excel 复制）</div>
              <textarea
                className="search"
                style={{ width: "100%", height: 160, resize: "none", fontSize: 13, fontFamily: "monospace", padding: 12 }}
                placeholder="每行一单：始发, 目的, 重量(吨), 件数, 联系电话（可用逗号或空格、Tab分隔）&#10;例：&#10;上海, 无锡, 5.5, 200, 13811112222&#10;无锡, 杭州, 10, 15, 13922223333"
                value={bulkText}
                onChange={(e) => setBulkText(e.target.value)}
              />
              <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
                <button
                  className="btn-primary"
                  disabled={validBulkRows.length === 0 || bulkImportMut.isPending}
                  onClick={() => bulkImportMut.mutate()}
                  style={{ padding: 12, flex: 1 }}
                >
                  {bulkImportMut.isPending ? "导入中…" : validBulkRows.length === 0 ? "无有效订单可导入" : `导入 ${validBulkRows.length} 张有效订单`}
                </button>
                {invalidBulkCount > 0 && (
                  <button className="btn-ghost" onClick={dropInvalidBulk} style={{ padding: 12, whiteSpace: "nowrap" }} title="移除缺失路线的行">
                    剔除 {invalidBulkCount} 条无效行
                  </button>
                )}
              </div>
              {invalidBulkCount > 0 && (
                <div className="small" style={{ color: "var(--amber)", display: "flex", alignItems: "center", gap: 6 }}>
                  <IconX size={12} className="icon-offset" />
                  {invalidBulkCount} 条缺始发/目的将被跳过；仅导入 {validBulkRows.length} 条有效行
                </div>
              )}
            </div>

            <div className="stack" style={{ gap: 10, background: "var(--panel-2)", padding: 16, borderRadius: 8, border: "1px solid var(--line)" }}>
              <div className="section-label">预览（{bulkRows.length} 行）</div>
              <div style={{ maxHeight: 160, overflowY: "auto", fontSize: 12 }}>
                <table className="table" style={{ width: "100%" }}>
                  <thead>
                    <tr>
                      <th>行</th><th>始发</th><th>目的</th><th>重量(吨)</th><th>件数</th><th>电话</th><th>状态</th>
                    </tr>
                  </thead>
                  <tbody>
                    {bulkRows.map((r) => (
                      <tr key={r.id} style={r.valid ? undefined : { background: "var(--red-bg)" }}>
                        <td>{r.id + 1}</td>
                        <td>{r.origin}</td>
                        <td>{r.destination}</td>
                        <td>{r.weight}t</td>
                        <td>{r.qty}件</td>
                        <td>{r.phone}</td>
                        <td>
                          {r.valid ? (
                            <span style={{ color: "var(--green)", fontWeight: 600 }}>✓ 正常</span>
                          ) : (
                            <span style={{ color: "var(--red)", fontWeight: 600 }}>✗ 缺失路线</span>
                          )}
                        </td>
                      </tr>
                    ))}
                    {bulkRows.length === 0 && (
                      <tr>
                        <td colSpan={7} style={{ textAlign: "center", padding: 12, color: "var(--text-soft)" }}>在左侧粘贴多行即可在此看到校验预览</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* === 模式三：标准专业 B2B 细案录单 === */}
      {activeMode === "standard" && (
        <div style={{ display: "flex", flexDirection: "column" }}>
          {/* 1. 基础关系与契约 */}
          <div className="form-section" style={{ padding: "18px 18px 0" }}>
            <div className="section-label">基础契约与客户信息</div>
            <div className="grid-form" style={{ marginTop: 12 }}>
              <label>订单来源
                <select value={form.channel} onChange={(e) => set("channel", e.target.value)}>
                  {Object.entries(ORDER_CHANNEL_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                </select>
              </label>
              <label>合同客户
                <select value={form.customer} onChange={(e) => { set("customer", e.target.value); onCustomerChange?.(e.target.value); }}>
                  <option value="">选择合同客户（可选）</option>
                  {(customers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                </select>
              </label>
              <label>客户分类
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
            </div>
          </div>

          {/* 2. 线路与上下游业务伙伴一键绑定 */}
          <div className="form-section" style={{ padding: "18px 18px 0" }}>
            <div className="section-label">线路与装卸网点</div>
            
            {/* 核心上下游实体快速对齐 */}
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 14, margin: "10px 0" }}>
              <div style={{ background: "var(--panel-2)", padding: 12, borderRadius: 8, border: "1px solid var(--line)" }}>
                <span className="muted small" style={{ fontWeight: "bold", color: "var(--primary)" }}>选择发货方 / 供应商</span>
                <select 
                  style={{ width: "100%", padding: "6px 8px", marginTop: 6, borderRadius: 6 }}
                  defaultValue="" 
                  onChange={(e) => { if (e.target.value) handlePartnerSelect("pickup", e.target.value); e.target.value = ""; }}
                >
                  <option value="">选择发货方…</option>
                  {shippers.map((s) => <option key={s.id} value={s.id}>[{s.city}] {s.name}</option>)}
                </select>
              </div>

              <div style={{ background: "var(--panel-2)", padding: 12, borderRadius: 8, border: "1px solid var(--line)" }}>
                <span className="muted small" style={{ fontWeight: "bold", color: "var(--primary)" }}>选择收货方 / 仓储网点</span>
                <select 
                  style={{ width: "100%", padding: "6px 8px", marginTop: 6, borderRadius: 6 }}
                  defaultValue="" 
                  onChange={(e) => { if (e.target.value) handlePartnerSelect("delivery", e.target.value); e.target.value = ""; }}
                >
                  <option value="">选择收货方…</option>
                  {consignees.map((c) => <option key={c.id} value={c.id}>[{c.city}] {c.name}</option>)}
                </select>
              </div>
            </div>

            <div className="grid-form">
              <label ref={originRef} className={showErrors && errs.origin ? "field-err" : ""}>始发城市 *<CityCombobox value={form.origin} onChange={(v) => set("origin", v)} placeholder="输入或选择，如 无锡" />{showErrors && errs.origin && <span className="field-err-hint">请填写始发城市</span>}</label>
              <label ref={destRef} className={showErrors && errs.destination ? "field-err" : ""}>目的城市 *<CityCombobox value={form.destination} onChange={(v) => set("destination", v)} placeholder="输入或选择，如 上海" />{showErrors && errs.destination && <span className="field-err-hint">请填写目的城市</span>}</label>
            </div>

            {form.customer && ((addressBook.data?.pickup.length ?? 0) > 0 || (addressBook.data?.delivery.length ?? 0) > 0) && (
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6, margin: "8px 0" }}>
                <span className="muted small">常用提送地址：</span>
                {(addressBook.data?.pickup ?? []).map((a, i) => (
                  <button key={`p${i}`} className="chip" onClick={() => fillStopFromBook("pickup", a)}>提·{a.city}{a.address ? ` ${a.address.slice(0, 8)}` : ""}</button>
                ))}
                {(addressBook.data?.delivery ?? []).map((a, i) => (
                  <button key={`d${i}`} className="chip" onClick={() => fillStopFromBook("delivery", a)}>送·{a.city}{a.address ? ` ${a.address.slice(0, 8)}` : ""}</button>
                ))}
              </div>
            )}

            {stops.map((s, i) => (
              <div key={i} className="line-row" style={{ marginTop: 8 }}>
                <select value={s.stop_type} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, stop_type: e.target.value as "pickup" | "delivery" } : x))}>
                  <option value="pickup">提货网点</option>
                  <option value="delivery">送货网点</option>
                </select>
                <RegionCascader
                  style={{ width: 190 }}
                  value={{ province: s.province ?? "", city: s.city, district: s.district ?? "" }}
                  onChange={(v) => setStops((p) => p.map((x, j) => j === i ? { ...x, province: v.province, city: v.city.replace(/(市|地区|自治州|盟)$/, "") || v.city, district: v.district } : x))}
                />
                <input placeholder="详细提/送货物理地址（街道门牌）" style={{ flex: 2 }} value={s.address} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, address: e.target.value } : x))} />
                <input placeholder="联系人" style={{ width: 90 }} value={s.contact_name} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, contact_name: e.target.value } : x))} />
                <input placeholder="电话" style={{ width: 130 }} value={s.contact_phone} onChange={(e) => setStops((p) => p.map((x, j) => j === i ? { ...x, contact_phone: e.target.value } : x))} />
                <button className="btn-ghost" onClick={() => setStops((p) => p.filter((_, j) => j !== i))} disabled={stops.length <= 1}>×</button>
              </div>
            ))}
            <button className="btn-ghost" style={{ marginTop: 6 }} onClick={() => setStops((p) => [...p, emptyStop("delivery")])}>+ 增加提送货网点</button>
          </div>

          {/* 3. 货物明细列表 */}
          <div ref={cargoRef} className="form-section" style={{ padding: "18px 18px 0" }}>
            <div className="section-label" style={{ display: "flex", justifyContent: "space-between", flexWrap: "wrap", gap: 10 }}>
              <span>货物明细 {showErrors && errs.cargo && <span className="field-err-hint">请至少填写一条货品名称</span>}</span>
              <span className="muted small">合计: {totalQty} 件 / {totalWeight.toFixed(2)} 吨 / {totalVolume.toFixed(2)} 方</span>
              {volumetric > totalWeight && <span className="tag tag-medium">抛重 {chargeable.toFixed(2)} 吨 计费</span>}
            </div>
            {cargo.map((c, i) => (
              <div key={i} className="line-row" style={{ marginTop: 8 }}>
                <input placeholder="货品名称 *" className={showErrors && errs.cargo && i === 0 ? "input-err" : ""} style={{ flex: 2 }} value={c.name} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, name: e.target.value } : x))} />
                <input placeholder="件数" style={{ width: 70 }} value={c.quantity} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, quantity: e.target.value } : x))} />
                <input placeholder="重量(吨)" style={{ width: 70 }} value={c.weight_ton} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, weight_ton: e.target.value } : x))} />
                <input placeholder="体积(方)" style={{ width: 70 }} value={c.volume_cbm} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, volume_cbm: e.target.value } : x))} />
                <input placeholder="包装" style={{ width: 80 }} value={c.package_type} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, package_type: e.target.value } : x))} />
                <input placeholder="冷链温区" style={{ width: 90 }} value={c.temperature_range} onChange={(e) => setCargo((p) => p.map((x, j) => j === i ? { ...x, temperature_range: e.target.value } : x))} />
                <button className="btn-ghost" onClick={() => setCargo((p) => p.filter((_, j) => j !== i))} disabled={cargo.length <= 1}>×</button>
              </div>
            ))}
            <button className="btn-ghost" style={{ marginTop: 6 }} onClick={() => setCargo((p) => [...p, emptyCargo()])}>+ 增加货物细项</button>
          </div>

          {/* 4. SLA 期望时效与报价 */}
          <div className="form-section" style={{ padding: "20px 18px 24px" }}>
            <div className="section-label" style={{ marginBottom: 16 }}>时效与运费</div>
            
            {/* 4.1 时效红线 (SLA) */}
            <div style={{ background: "var(--panel-2)", padding: "14px 16px", borderRadius: 8, border: "1px solid var(--line)", marginBottom: 16 }}>
              <div style={{ fontSize: 12, fontWeight: "bold", color: "var(--ink-2)", marginBottom: 10 }}>时效要求</div>
              <div className="grid-form" style={{ gridTemplateColumns: "1fr 1fr", gap: 16 }}>
                <label>
                  期望提货窗口
                  <DateTimeField value={form.expected_pickup_at} onChange={(v) => set("expected_pickup_at", v)} />
                </label>
                <label>
                  期望送达时间
                  <DateTimeField value={form.expected_delivery_at} onChange={(v) => set("expected_delivery_at", v)} style={{ borderColor: form.expected_delivery_at ? "var(--amber-border)" : "var(--line-strong)" }} />
                </label>
              </div>
            </div>

            {/* 4.2 财务核价 (Financial) */}
            <div style={{ background: "var(--panel)", padding: "14px 16px", borderRadius: 8, border: "1px solid var(--line)", marginBottom: 16 }}>
              <div style={{ fontSize: 12, fontWeight: "bold", color: "var(--ink-2)", marginBottom: 10 }}>运费与货值</div>
              <div className="grid-form" style={{ gridTemplateColumns: "1.5fr 1fr", gap: 16 }}>
                <label>
                  <span style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <span>合同/预估运费 (¥)</span>
                    <button 
                      className="btn-ghost" 
                      style={{ padding: "2px 8px", fontSize: 11, color: "var(--brand)", background: "var(--brand-light)", border: "none" }} 
                      disabled={quote.isPending} 
                      onClick={() => quote.mutate()}
                    >
                      {quote.isPending ? "计算中…" : "计算运费"}
                    </button>
                  </span>
                  <input className="search" style={{ fontSize: 14, fontWeight: "bold", color: form.quoted_amount ? "var(--green)" : "inherit" }} placeholder="0.00" value={form.quoted_amount} onChange={(e) => set("quoted_amount", e.target.value)} />
                </label>
                <label>
                  投保声明货值 (¥)
                  <input className="search" placeholder="0.00" value={form.cargo_value} onChange={(e) => set("cargo_value", e.target.value)} />
                </label>
              </div>
              <div className="grid-form" style={{ gridTemplateColumns: "1fr 1fr 1fr", gap: 16, marginTop: 12 }}>
                <label>
                  运费付款方式
                  <select className="search" value={form.freight_term} onChange={(e) => set("freight_term", e.target.value)}>
                    <option value="prepaid">现付</option>
                    <option value="collect">到付</option>
                    <option value="receipt">回单付</option>
                    <option value="monthly">月结</option>
                  </select>
                </label>
                <label>
                  运费承担方
                  <select className="search" value={form.freight_payer} onChange={(e) => set("freight_payer", e.target.value)}>
                    <option value="shipper">发货方</option>
                    <option value="consignee">收货方</option>
                    <option value="third_party">第三方</option>
                  </select>
                </label>
                <label>
                  代收货款 COD (¥)
                  <input className="search" placeholder="0.00 无则留空" value={form.cod_amount} onChange={(e) => set("cod_amount", e.target.value)} />
                </label>
              </div>
            </div>

            {/* 4.3 特种保障 (Special Care) */}
            <div style={{ background: "var(--panel)", padding: "14px 16px", borderRadius: 8, border: "1px dashed var(--line-strong)" }}>
              <div style={{ fontSize: 12, fontWeight: "bold", color: "var(--ink-2)", marginBottom: 10 }}>特种运输要求</div>
              <div className="grid-form" style={{ gridTemplateColumns: "1fr 1fr auto", gap: 16, alignItems: "end" }}>
                <label>
                  包装标准
                  <input className="search" placeholder="例: 托盘 / 木箱 / 裸装" value={form.package_type} onChange={(e) => set("package_type", e.target.value)} />
                </label>
                <label>
                  温控区间 (冷链)
                  <input className="search" placeholder="例: -18~0℃ 或 2~8℃" value={form.temperature_range} onChange={(e) => set("temperature_range", e.target.value)} />
                </label>
                <label className="switch-mini" style={{ padding: "8px 12px", background: form.is_hazardous ? "var(--red-bg)" : "var(--panel-2)", borderRadius: 6, border: `1px solid ${form.is_hazardous ? "var(--red-border)" : "var(--line)"}`, color: form.is_hazardous ? "var(--red)" : "var(--ink-2)" }}>
                  <input type="checkbox" style={{ accentColor: "var(--red)" }} checked={form.is_hazardous} onChange={(e) => set("is_hazardous", e.target.checked)} /> 
                  <strong style={{ marginLeft: 4 }}>危化品 / 高危品</strong>
                </label>
              </div>
              <textarea className="search" style={{ width: "100%", minHeight: 64, marginTop: 16, resize: "vertical" }} placeholder="其他特殊作业、运送、签收、回单等备注要求…" value={form.remark} onChange={(e) => set("remark", e.target.value)} />
            </div>
          </div>

          {/* 5. 表单提交控制 */}
          <div className="form-actions" style={{ padding: "0 18px 20px", display: "flex", gap: 10, alignItems: "center", borderTop: "1px solid var(--line)", paddingTop: 14 }}>
            <button className="btn-primary" style={{ padding: "10px 24px" }} disabled={submit.isPending} onClick={() => trySubmit("pending_confirm")}>确认提交</button>
            <button className="btn-ghost" disabled={submit.isPending} onClick={() => trySubmit("draft")}>暂存草稿</button>
            <button className="btn-ghost" onClick={reset}>清空</button>
            <label className="switch-mini" title="提交后保留客户/来源等抬头，仅清空货物与线路，便于连续录单">
              <input type="checkbox" checked={continuous} onChange={(e) => setContinuous(e.target.checked)} /> 连续建单
            </label>
            <span className="muted small" style={{ marginLeft: 4 }}>Ctrl+Enter 提交</span>
            <span style={{ flex: 1 }} />
            <input placeholder="另存为新订单模板名" style={{ width: 160 }} value={tplName} onChange={(e) => setTplName(e.target.value)} />
            <button className="btn-ghost" disabled={!tplName.trim() || saveTpl.isPending} onClick={() => saveTpl.mutate()}>存为模板</button>
          </div>
          {showErrors && !valid && (
            <div className="small" style={{ padding: "0 18px 14px", color: "var(--red)", display: "flex", alignItems: "center", gap: 6 }}>
              <IconX size={13} className="icon-offset" />
              还需补全：{[errs.origin && "始发城市", errs.destination && "目的城市", errs.cargo && "货品名称"].filter(Boolean).join("、")}
            </div>
          )}
        </div>
      )}
    </div>
  );
}