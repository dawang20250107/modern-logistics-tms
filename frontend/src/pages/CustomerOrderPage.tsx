import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";

import { apiPost } from "../api/client";
import { IconCheckCircle } from "../components/Icons";

interface Form {
  contact_name: string;
  contact_phone: string;
  origin: string;
  destination: string;
  cargo_desc: string;
  cargo_weight_ton: string;
  cargo_quantity: string;
  expected_pickup_at: string;
  remark: string;
}
const EMPTY: Form = {
  contact_name: "", contact_phone: "", origin: "", destination: "", cargo_desc: "",
  cargo_weight_ton: "", cargo_quantity: "", expected_pickup_at: "", remark: "",
};

export function CustomerOrderPage() {
  const [form, setForm] = useState<Form>(EMPTY);
  const [done, setDone] = useState<string | null>(null);
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm((f) => ({ ...f, [k]: v }));

  const submit = useMutation({
    mutationFn: () => apiPost<{ order_no: string; message: string }>("/public/orders", {
      channel: "self",
      contact_name: form.contact_name,
      contact_phone: form.contact_phone,
      origin: form.origin,
      destination: form.destination,
      cargo_desc: form.cargo_desc,
      cargo_weight_ton: form.cargo_weight_ton || 0,
      cargo_quantity: form.cargo_quantity || 0,
      expected_pickup_at: form.expected_pickup_at || undefined,
      remark: form.remark,
    }),
    onSuccess: (d) => setDone(d.order_no),
  });

  const valid = form.contact_phone.trim() && form.origin.trim() && form.destination.trim();

  return (
    <div className="public-page">
      <div className="public-card">
        <div className="public-brand">在线下单</div>
        {done ? (
          <div className="stack" style={{ textAlign: "center", gap: 14, padding: "10px 0" }}>
            <div className="public-success-mark" aria-hidden="true"><IconCheckCircle size={30} /></div>
            <div style={{ fontSize: 18, fontWeight: 700 }}>下单成功</div>
            <div className="mono" style={{ fontSize: 16 }}>{done}</div>
            <div className="muted">客服将尽快与您电话确认。请保存订单号，可凭订单号 + 手机号查询进度。</div>
            <div style={{ display: "flex", gap: 10, justifyContent: "center" }}>
              <Link className="btn-primary" to={`/track?order_no=${done}`} style={{ textDecoration: "none" }}>查询进度</Link>
              <button type="button" className="btn-ghost" onClick={() => { setDone(null); setForm(EMPTY); }}>再下一单</button>
            </div>
          </div>
        ) : (
          <>
            <p className="muted small" style={{ marginTop: -6 }}>填写下方信息提交运输需求，客服确认后为您安排车辆。</p>
            <div className="grid-form">
              <label>联系人<input value={form.contact_name} onChange={(e) => set("contact_name", e.target.value)} placeholder="您的称呼" /></label>
              <label>联系电话 *<input value={form.contact_phone} onChange={(e) => set("contact_phone", e.target.value)} placeholder="手机号" inputMode="tel" autoComplete="tel" /></label>
              <label>始发地 *<input value={form.origin} onChange={(e) => set("origin", e.target.value)} placeholder="如 上海" /></label>
              <label>目的地 *<input value={form.destination} onChange={(e) => set("destination", e.target.value)} placeholder="如 成都" /></label>
              <label>货物名称<input value={form.cargo_desc} onChange={(e) => set("cargo_desc", e.target.value)} placeholder="如 电子配件" /></label>
              <label>重量(吨)<input value={form.cargo_weight_ton} onChange={(e) => set("cargo_weight_ton", e.target.value)} /></label>
              <label>件数<input value={form.cargo_quantity} onChange={(e) => set("cargo_quantity", e.target.value)} /></label>
              <label>期望提货时间<input type="datetime-local" value={form.expected_pickup_at} onChange={(e) => set("expected_pickup_at", e.target.value)} /></label>
            </div>
            <textarea className="search" style={{ width: "100%", minHeight: 60, marginTop: 10 }} placeholder="备注 / 特殊要求" value={form.remark} onChange={(e) => set("remark", e.target.value)} />
            <button className="btn-primary" style={{ width: "100%", marginTop: 14 }} disabled={!valid || submit.isPending} onClick={() => submit.mutate()}>
              {submit.isPending ? "提交中…" : "提交下单"}
            </button>
            {submit.isError && <div className="login-error" role="alert">提交失败，请检查网络后重试。</div>}
            {!valid && <div className="muted small" style={{ marginTop: 8, textAlign: "center" }}>请填写联系电话、始发地、目的地</div>}
            <div style={{ textAlign: "center", marginTop: 14 }}>
              <Link className="link small" to="/track">已下单？查询进度 →</Link>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
