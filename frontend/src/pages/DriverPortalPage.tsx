import { useCallback, useEffect, useState } from "react";

import { API_BASE_URL } from "../api/client";
import { toast } from "../api/toast";

interface Reminder { id: string; title: string; content: string; ack_required: boolean; waybill_no: string }
interface WaybillBrief { waybill_no: string; route_name: string; origin: string; destination: string; status: string }
interface Tasks { driver: { name: string; phone: string }; waybills: WaybillBrief[]; pending_reminders: Reminder[] }

const NODES: [string, string][] = [
  ["depart", "出发"], ["arrive_pickup", "到达装货地"], ["queuing", "排队"], ["loading", "装货"],
  ["depart_loaded", "发车"], ["in_transit", "在途打卡"], ["arrive_delivery", "到达卸货地"],
  ["unloading", "卸货"], ["receipt", "回单"], ["finish", "订单结束"],
];
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
    const rest = (tasks?.pending_reminders ?? []).filter((x) => x.id !== r.id);
    setTasks((t) => t ? { ...t, pending_reminders: rest } : t);
    setActive(rest[0] ?? null);
    toast.success("已确认收到");
  }

  if (!token) {
    return (
      <div className="public-page"><div className="public-card driver-card">
        <div className="public-brand">智运 · 司机端</div>
        <p className="muted small">手机号 + 身份证后6位登录</p>
        <div className="grid-form">
          <label>手机号<input value={phone} onChange={(e) => setPhone(e.target.value)} inputMode="tel" /></label>
          <label>身份证后6位<input value={idTail} onChange={(e) => setIdTail(e.target.value)} /></label>
        </div>
        <button className="btn-primary" style={{ width: "100%", marginTop: 14 }} disabled={!phone && !idTail} onClick={login}>登 录</button>
      </div></div>
    );
  }

  return (
    <div className="public-page" style={{ alignItems: "flex-start" }}>
      <div className="public-card driver-card">
        <div className="public-brand">智运 · 司机端</div>
        <div className="muted small" style={{ marginBottom: 10 }}>
          {tasks?.driver.name} · {tasks?.driver.phone}
          <button className="link small" style={{ float: "right" }} onClick={() => { localStorage.removeItem("driver_token"); setToken(""); setTasks(null); }}>退出</button>
        </div>

        {(tasks?.waybills.length ?? 0) === 0 && <div className="muted small">暂无在途任务。</div>}
        {(tasks?.waybills ?? []).map((w) => (
          <WaybillCard key={w.waybill_no} wb={w} token={token} />
        ))}

        <CredentialUpload token={token} />
      </div>

      {active && (
        <div className="driver-modal-mask">
          <div className="driver-modal">
            <div className="driver-modal-title">⚠️ 作业提醒（必读）</div>
            <div className="driver-modal-sub">{active.title}{active.waybill_no ? ` · ${active.waybill_no}` : ""}</div>
            <pre className="driver-modal-body">{active.content}</pre>
            <button className="btn-primary" style={{ width: "100%" }} onClick={() => ackReminder(active)}>
              {active.ack_required ? "我已阅读并确认收到" : "确认"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function WaybillCard({ wb, token }: { wb: WaybillBrief; token: string }) {
  const [node, setNode] = useState("depart");
  const [busy, setBusy] = useState(false);

  async function checkin(file?: File) {
    setBusy(true);
    try {
      const pos = await new Promise<GeolocationPosition | null>((res) =>
        navigator.geolocation ? navigator.geolocation.getCurrentPosition((p) => res(p), () => res(null), { timeout: 5000 }) : res(null));
      const fd = new FormData();
      fd.append("waybill_no", wb.waybill_no);
      fd.append("node", node);
      if (pos) { fd.append("lat", String(pos.coords.latitude)); fd.append("lng", String(pos.coords.longitude)); }
      if (file) fd.append("photo", file);
      await dFetch("/driver/checkin", token, { method: "POST", body: fd });
      toast.success(`已打卡：${NODES.find((n) => n[0] === node)?.[1]}`);
    } catch (e) { toast.error(e instanceof Error ? e.message : "打卡失败"); }
    finally { setBusy(false); }
  }

  return (
    <div className="driver-wb">
      <div className="driver-wb-head"><b>{wb.waybill_no}</b><span className="muted small">{wb.route_name}</span></div>
      <div className="form-row" style={{ gap: 8, flexWrap: "wrap", padding: 0 }}>
        <select value={node} onChange={(e) => setNode(e.target.value)}>
          {NODES.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <button className="btn-ghost" disabled={busy} onClick={() => checkin()}>仅定位打卡</button>
        <label className="btn-primary" style={{ cursor: "pointer" }}>
          {busy ? "提交中…" : "拍照打卡"}
          <input type="file" accept="image/*" capture="environment" style={{ display: "none" }}
                 onChange={(e) => { const f = e.target.files?.[0]; if (f) checkin(f); e.target.value = ""; }} />
        </label>
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
        <label className="btn-ghost" style={{ cursor: "pointer" }}>
          {busy ? "上传中…" : "选择照片"}
          <input type="file" accept="image/*" capture="environment" style={{ display: "none" }}
                 onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = ""; }} />
        </label>
      </div>
    </div>
  );
}
