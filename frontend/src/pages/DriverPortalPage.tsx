import { useCallback, useEffect, useRef, useState } from "react";

import { API_BASE_URL } from "../api/client";
import { fmtMoney } from "../api/format";
import { useModalA11y } from "../api/useModalA11y";
import { toast } from "../api/toast";
import { StateView } from "../components/StateView";
import { StatusTag } from "../components/StatusTag";

interface Reminder { id: string; title: string; content: string; level?: string; ack_required: boolean; waybill_no: string }

// 调度指令分级：普通蓝(信息) / 重要琥珀(注意) / 紧急红(必须确认)
const CMD_LEVEL: Record<string, { grad: string; solid: string; tag: string; label: string }> = {
  normal: { grad: "linear-gradient(135deg,var(--blue),#1e50c0)", solid: "var(--blue)", tag: "普通指令", label: "确认" },
  important: { grad: "linear-gradient(135deg,#b8860b,#8a6508)", solid: "var(--amber)", tag: "重要指令", label: "我已知悉" },
  urgent: { grad: "linear-gradient(135deg,var(--red),#a81d24)", solid: "var(--red)", tag: "紧急指令", label: "我已阅读并严格执行" },
};
interface NextStep { node: string; label: string; kind: string }
interface WaybillBrief {
  waybill_no: string; route_name: string; origin: string; destination: string; status: string;
  status_label: string; next_step: NextStep | null;
  pickup_address: string; delivery_address: string; pickup_contact_phone: string; delivery_contact_phone: string;
  cod_amount: number;
}
interface Tasks { driver: { name: string; phone: string }; waybills: WaybillBrief[]; pending_reminders: Reminder[] }

const CRED_TYPES: [string, string][] = [
  ["vehicle_license", "车头行驶证"], ["trailer_license", "车挂行驶证"], ["driving_license", "驾驶证"],
  ["transport_cert", "道路运输证"], ["id_card", "身份证"],
];

async function dFetch(path: string, token: string, init: RequestInit = {}) {
  const headers = new Headers(init.headers ?? {});
  if (token) headers.set("X-Driver-Token", token);
  const resp = await fetch(`${API_BASE_URL}${path}`, { ...init, headers });
  const json = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(json?.error?.message || json?.detail || "请求失败");
  return json.data ?? json;
}

export function DriverPortalPage() {
  const [token, setToken] = useState(() => localStorage.getItem("driver_token") || "");
  const [phone, setPhone] = useState("");
  const [idTail, setIdTail] = useState("");
  const [tasks, setTasks] = useState<Tasks | null>(null);
  const [active, setActive] = useState<Reminder | null>(null);
  const cmdRef = useRef<HTMLDivElement>(null);
  useModalA11y(Boolean(active), cmdRef, () => setActive(null));

  const loadTasks = useCallback(async (tk: string) => {
    try {
      const data: Tasks = await dFetch("/driver/tasks", tk);
      setTasks(data);
      if (data.pending_reminders.length > 0) setActive(data.pending_reminders[0]);
    } catch (e) {
      setToken(""); localStorage.removeItem("driver_token");
      toast.error(e instanceof Error ? e.message : "登录已过期");
    }
  }, []);

  useEffect(() => { if (token) loadTasks(token); }, [token, loadTasks]);

  async function login() {
    try {
      const data = await dFetch("/driver/login", "", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ phone, id_tail: idTail }),
      });
      localStorage.setItem("driver_token", data.token);
      setToken(data.token);
    } catch (e) { toast.error(e instanceof Error ? e.message : "登录失败"); }
  }

  async function ackReminder(r: Reminder) {
    await dFetch(`/driver/reminders/${r.id}/ack`, token, { method: "POST" });
    const rest = (tasks?.pending_reminders ?? [])?.filter((x) => x.id !== r.id);
    setTasks((t) => t ? { ...t, pending_reminders: rest } : t);
    setActive(rest[0] ?? null);
    toast.success("已确认收到");
  }

  if (!token) {
    return (
      <div className="public-page">
        <div className="public-card driver-card" style={{ padding: "40px 30px" }}>
          <div className="drv-login-hero">
            <div className="drv-login-badge" />
            <div className="public-brand" style={{ fontSize: 24 }}>智运 · 司机端</div>
            <p className="muted" style={{ fontSize: 13, marginTop: 4 }}>手机号 + 身份证后6位 安全登录</p>
          </div>
          <div className="drv-login-fields">
            <label>手机号
              <input value={phone} onChange={(e) => setPhone(e.target.value)} inputMode="tel" placeholder="请输入预留的手机号" />
            </label>
            <label>身份证后 6 位
              <input value={idTail} onChange={(e) => setIdTail(e.target.value)} type="password" placeholder="请输入身份证最后6位数字" style={{ letterSpacing: 4 }} />
            </label>
          </div>
          <button className="btn-primary drv-login-submit" disabled={!phone || idTail.length !== 6} onClick={login}>安全登录</button>
        </div>
      </div>
    );
  }

  return (
    <div className="public-page" style={{ alignItems: "flex-start" }}>
      <div className="public-card driver-card" style={{ padding: 0, overflow: "hidden" }}>
        {/* 顶部司机身份面板 */}
        <div className="drv-topbar">
          <div className="cluster-between">
            <div className="cluster" style={{ gap: 14 }}>
              <div className="drv-avatar">{tasks?.driver.name?.[0] ?? "司"}</div>
              <div className="stack-sm" style={{ gap: 4 }}>
                <span className="drv-name">{tasks?.driver.name}</span>
                <span className="drv-phone">{tasks?.driver.phone}</span>
              </div>
            </div>
            <button className="drv-logout" onClick={() => { localStorage.removeItem("driver_token"); setToken(""); setTasks(null); }}>退出</button>
          </div>
        </div>

        {/* 任务流与打卡区 */}
        <div className="drv-body">
          <div className="drv-body-head">
            <h3>在途运输任务</h3>
            <span className="tag tag-info">{tasks?.waybills.length ?? 0} 单进行中</span>
          </div>

          {(tasks?.waybills.length ?? 0) === 0 ? (
            <div style={{ background: "var(--panel)", borderRadius: 12, border: "1px dashed var(--line-2)" }}>
              <StateView kind="empty" scene="driver-empty" />
            </div>
          ) : (
            <div className="stack-md">
              {(tasks?.waybills ?? []).map((w) => (
                <WaybillCard key={w.waybill_no} wb={w} token={token} />
              ))}
            </div>
          )}

          <div style={{ marginTop: 24 }}>
            <CredentialUpload token={token} />
          </div>
        </div>
      </div>

      {/* 调度指令 Modal（按分级着色：普通蓝/重要琥珀/紧急红） */}
      {active && (() => {
        const lv = CMD_LEVEL[active.level ?? "important"] ?? CMD_LEVEL.important;
        return (
          <div className="driver-modal-mask" style={{ backdropFilter: "blur(4px)" }}>
            <div ref={cmdRef} tabIndex={-1} role="dialog" aria-modal="true" aria-label={`调度指令 ${lv.tag}`} className="driver-modal" style={{ padding: 0, overflow: "hidden" }}>
              <div style={{ background: lv.grad, color: "var(--hero-ink)", padding: "20px 24px" }}>
                <div className="driver-modal-title" style={{ color: "#fff", display: "flex", alignItems: "center", gap: 8 }}>
                  调度中心 · {lv.tag}
                </div>
                <div style={{ fontSize: 13, opacity: 0.85, marginTop: 4 }}>{active.title}{active.waybill_no ? ` · ${active.waybill_no}` : ""}</div>
              </div>
              <div style={{ padding: 24 }}>
                <div style={{ background: "var(--panel-2)", color: "var(--ink)", padding: 16, borderRadius: 12, fontSize: 14, lineHeight: 1.6, fontWeight: 600, borderLeft: `4px solid ${lv.solid}` }}>
                  {active.content}
                </div>
                <button
                  className="btn-primary"
                  style={{ width: "100%", marginTop: 24, padding: 14, fontSize: 15, background: lv.solid, boxShadow: `0 4px 12px ${lv.solid}44` }}
                  onClick={() => ackReminder(active)}
                >
                  ✓ {active.ack_required ? lv.label : "确认"}
                </button>
              </div>
            </div>
          </div>
        );
      })()}
    </div>
  );
}

type Phase = "idle" | "locating" | "uploading" | "done" | "error";

function WaybillCard({ wb, token }: { wb: WaybillBrief; token: string }) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [feedback, setFeedback] = useState("");
  const [lastFile, setLastFile] = useState<File | undefined>(undefined);
  const step = wb.next_step;
  const busy = phase === "locating" || phase === "uploading";

  async function checkin(file?: File) {
    if (!step) return;
    setLastFile(file);
    // 弱网友好：定位失败也可继续提交，每一步都有明确反馈
    setPhase("locating");
    setFeedback("正在定位…");
    const pos = await new Promise<GeolocationPosition | null>((res) =>
      navigator.geolocation ? navigator.geolocation.getCurrentPosition((p) => res(p), () => res(null), { timeout: 5000 }) : res(null));
    if (!pos) setFeedback("定位失败，仍可继续提交");
    setPhase("uploading");
    setFeedback(file ? "照片上传中…" : "提交中…");
    try {
      const fd = new FormData();
      fd.append("waybill_no", wb.waybill_no);
      fd.append("node", step.node);
      if (pos) { fd.append("lat", String(pos.coords.latitude)); fd.append("lng", String(pos.coords.longitude)); }
      if (file) fd.append("photo", file);
      await dFetch("/driver/checkin", token, { method: "POST", body: fd });
      setPhase("done");
      setFeedback(`${step.label} · 已完成，已通知调度`);
      toast.success(`${step.label} · 已完成`);
      setTimeout(() => window.location.reload(), 900);
    } catch (e) {
      setPhase("error");
      setFeedback(e instanceof Error ? e.message : "网络异常，提交失败");
    }
  }

  const navTo = wb.delivery_address || wb.destination;

  return (
    <div className="drv-card">
      {/* 运单状态头 */}
      <div className="drv-card-head">
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <b style={{ fontSize: 16 }}>{wb.origin || "?"} → {wb.destination || "?"}</b>
          <span className="muted mono" style={{ fontSize: 12 }}>{wb.waybill_no}</span>
        </div>
        <StatusTag kind="waybill" value={wb.status} title={wb.status_label} />
      </div>

      {/* 地址区 */}
      <div className="drv-addr">
        {wb.pickup_address && <div><span className="drv-dot drv-dot-o" />装：{wb.pickup_address}</div>}
        {wb.delivery_address && <div><span className="drv-dot drv-dot-d" />卸：{wb.delivery_address}</div>}
        {wb.cod_amount > 0 && <div className="drv-cod">需代收货款 {fmtMoney(wb.cod_amount)}，请向收货人收齐后确认</div>}
      </div>

      {/* 下一步：单主按钮（拍照打卡） */}
      <div style={{ padding: 16 }}>
        {step ? (
          <>
            <label className="file-trigger" style={{ display: "block" }}>
              <div className="drv-main-btn">
                {busy ? "上传中…" : `${step.label} · 拍照打卡`}
              </div>
              <input className="file-input-accessible" type="file" accept="image/*" capture="environment" disabled={busy}
                     onChange={(e) => { const f = e.target.files?.[0]; if (f) checkin(f); e.target.value = ""; }} />
            </label>
            <button className="drv-sub-btn" disabled={busy} onClick={() => checkin()}>
              {busy ? "处理中…" : `无需拍照，直接${step.label}`}
            </button>
            {/* 弱网反馈：每一步都有明确状态 + 失败可重试 */}
            {phase !== "idle" && (
              <div className={`drv-feedback drv-fb-${phase}`}>
                <span>{phase === "done" ? "✓ " : phase === "error" ? "✗ " : ""}{feedback}</span>
                {phase === "error" && <button className="drv-retry" onClick={() => checkin(lastFile)}>重试</button>}
              </div>
            )}
          </>
        ) : (
          <div className="muted small" style={{ textAlign: "center", padding: 8 }}>本单已完成，无需进一步操作。</div>
        )}

        {/* 快捷：导航 / 联系 */}
        <div className="drv-quick">
          {navTo && <a className="drv-quick-btn" href={`https://uri.amap.com/search?keyword=${encodeURIComponent(navTo)}`} target="_blank" rel="noreferrer">导航</a>}
          {wb.delivery_contact_phone && <a className="drv-quick-btn" href={`tel:${wb.delivery_contact_phone}`}>联系收货人</a>}
          {wb.pickup_contact_phone && <a className="drv-quick-btn" href={`tel:${wb.pickup_contact_phone}`}>联系发货人</a>}
        </div>
      </div>
    </div>
  );
}

function CredentialUpload({ token }: { token: string }) {
  const [credType, setCredType] = useState("driving_license");
  const [side, setSide] = useState("main");
  const [busy, setBusy] = useState(false);

  async function upload(file: File) {
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("cred_type", credType); fd.append("side", side); fd.append("file", file);
      await dFetch("/driver/credentials", token, { method: "POST", body: fd });
      toast.success("证件已上传，识别建档中");
    } catch (e) { toast.error(e instanceof Error ? e.message : "上传失败"); }
    finally { setBusy(false); }
  }

  return (
    <div className="driver-wb">
      <div className="driver-wb-head"><b>证件上传</b><span className="muted small">自助上传建档</span></div>
      <div className="form-row" style={{ gap: 8, flexWrap: "wrap", padding: 0 }}>
        <select value={credType} onChange={(e) => setCredType(e.target.value)}>
          {CRED_TYPES.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <select value={side} onChange={(e) => setSide(e.target.value)}>
          <option value="main">主页/正面</option><option value="back">副页/反面</option>
        </select>
        <label className="btn-ghost file-trigger" style={{ cursor: "pointer" }}>
          {busy ? "上传中…" : "选择照片"}
          <input className="file-input-accessible" type="file" accept="image/*" capture="environment" disabled={busy}
                 onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = ""; }} />
        </label>
      </div>
    </div>
  );
}
