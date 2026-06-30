import { useCallback, useEffect, useState } from "react";

import { API_BASE_URL } from "../api/client";
import { toast } from "../api/toast";

interface Reminder { id: string; title: string; content: string; ack_required: boolean; waybill_no: string }
interface WaybillBrief { waybill_no: string; route_name: string; origin: string; destination: string; status: string }
interface Tasks { driver: { name: string; phone: string }; waybills: WaybillBrief[]; pending_reminders: Reminder[] }

const NODES: [string, string][] = [
  ["depart", "🛫 出发"], ["arrive_pickup", "📍 到达装货地"], ["queuing", "⏳ 排队"], ["loading", "📦 装货"],
  ["depart_loaded", "🚚 满载发车"], ["in_transit", "🛣️ 在途打卡"], ["arrive_delivery", "📍 到达卸货地"],
  ["unloading", "📥 卸货"], ["receipt", "🧾 回单签收"], ["finish", "✅ 订单结束"],
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
    const rest = (tasks?.pending_reminders ?? [])?.filter((x) => x.id !== r.id);
    setTasks((t) => t ? { ...t, pending_reminders: rest } : t);
    setActive(rest[0] ?? null);
    toast.success("已确认收到");
  }

  if (!token) {
    return (
      <div className="public-page" style={{ background: "#f8fafc", padding: "10vh 20px" }}>
        <div className="public-card driver-card" style={{ padding: "40px 30px", border: "none", boxShadow: "0 20px 40px rgba(0,0,0,0.08)" }}>
          <div style={{ textAlign: "center", marginBottom: 30 }}>
            <div style={{ width: 64, height: 64, background: "var(--grad)", borderRadius: 16, margin: "0 auto 16px", display: "grid", placeItems: "center", fontSize: 32, boxShadow: "0 10px 20px rgba(37,99,235,0.3)" }}>🚛</div>
            <div className="public-brand" style={{ fontSize: 24, letterSpacing: 1 }}>智运 · 司机端</div>
            <p className="muted" style={{ fontSize: 13, marginTop: 4 }}>手机号 + 身份证后6位 安全登录</p>
          </div>
          
          <div className="grid-form" style={{ display: "flex", flexDirection: "column", gap: 16 }}>
            <label style={{ fontSize: 13, fontWeight: "bold" }}>
              手机号
              <input 
                value={phone} onChange={(e) => setPhone(e.target.value)} inputMode="tel" 
                placeholder="请输入预留的手机号"
                style={{ padding: "14px 16px", fontSize: 16, borderRadius: 12, marginTop: 6, background: "#f1f5f9", border: "1px solid transparent" }} 
              />
            </label>
            <label style={{ fontSize: 13, fontWeight: "bold" }}>
              身份证后 6 位
              <input 
                value={idTail} onChange={(e) => setIdTail(e.target.value)} type="password" 
                placeholder="请输入身份证最后6位数字"
                style={{ padding: "14px 16px", fontSize: 16, borderRadius: 12, marginTop: 6, background: "#f1f5f9", border: "1px solid transparent", letterSpacing: 4 }} 
              />
            </label>
          </div>
          <button 
            className="btn-primary" 
            style={{ width: "100%", marginTop: 30, padding: 16, fontSize: 16, borderRadius: 12, fontWeight: "bold" }} 
            disabled={!phone || idTail.length !== 6}
            onClick={login}
          >
            安 全 登 录
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="public-page" style={{ alignItems: "flex-start", padding: "20px 16px", background: "#f1f5f9" }}>
      <div className="public-card driver-card" style={{ padding: 0, overflow: "hidden", border: "none", boxShadow: "0 10px 30px rgba(0,0,0,0.06)" }}>
        {/* 顶部深色司机身份面板 */}
        <div style={{ background: "linear-gradient(135deg, #1e293b 0%, #0f172a 100%)", color: "#fff", padding: "24px 20px" }}>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
              <div style={{ width: 48, height: 48, borderRadius: "50%", background: "rgba(255,255,255,0.1)", display: "flex", alignItems: "center", justifyContent: "center", fontSize: 22, border: "2px solid rgba(255,255,255,0.2)" }}>
                👨‍✈️
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                <span style={{ fontSize: 18, fontWeight: "bold", letterSpacing: 1 }}>{tasks?.driver.name}</span>
                <span style={{ fontSize: 13, color: "rgba(255,255,255,0.6)", fontFamily: "monospace" }}>{tasks?.driver.phone}</span>
              </div>
            </div>
            <button 
              className="btn-ghost" 
              style={{ background: "rgba(255,255,255,0.1)", border: "none", color: "#fff", padding: "6px 12px", borderRadius: 20, fontSize: 12 }} 
              onClick={() => { localStorage.removeItem("driver_token"); setToken(""); setTasks(null); }}
            >
              退出
            </button>
          </div>
        </div>

        {/* 任务流与打卡区 */}
        <div style={{ padding: 20 }}>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
            <h3 style={{ margin: 0, fontSize: 16, color: "var(--ink)" }}>🚚 在途运输任务</h3>
            <span className="tag" style={{ background: "rgba(37,99,235,0.1)", color: "var(--brand)", fontWeight: "bold" }}>
              {tasks?.waybills.length ?? 0} 单进行中
            </span>
          </div>

          {(tasks?.waybills.length ?? 0) === 0 ? (
            <div style={{ background: "#fff", padding: 30, borderRadius: 12, textAlign: "center", border: "1px dashed var(--line-strong)" }}>
              <div style={{ fontSize: 32, opacity: 0.5, marginBottom: 10 }}>🏖️</div>
              <div className="muted" style={{ fontSize: 14 }}>当前暂无在途任务，请等待调度派单。</div>
            </div>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
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

      {/* AI 与调度 主动下发的任务强提醒 Modal */}
      {active && (
        <div className="driver-modal-mask" style={{ backdropFilter: "blur(4px)" }}>
          <div className="driver-modal" style={{ padding: 0, overflow: "hidden" }}>
            <div style={{ background: "linear-gradient(135deg, #e74c3c 0%, #c0392b 100%)", color: "#fff", padding: "20px 24px" }}>
              <div className="driver-modal-title" style={{ color: "#fff", display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontSize: 22 }}>⚠️</span> 调度中心指令 (必读)
              </div>
              <div style={{ fontSize: 13, opacity: 0.8, marginTop: 4 }}>{active.title}{active.waybill_no ? ` · ${active.waybill_no}` : ""}</div>
            </div>
            
            <div style={{ padding: 24 }}>
              <div style={{ background: "#fff5f5", color: "#c0392b", padding: 16, borderRadius: 12, fontSize: 14, lineHeight: 1.6, fontWeight: "bold", borderLeft: "4px solid #e74c3c" }}>
                {active.content}
              </div>
              <button 
                className="btn-primary" 
                style={{ width: "100%", marginTop: 24, padding: 14, fontSize: 15, background: "#e74c3c", boxShadow: "0 4px 12px rgba(231, 76, 60, 0.3)" }} 
                onClick={() => ackReminder(active)}
              >
                {active.ack_required ? "✓ 我已阅读并严格执行" : "✓ 确认"}
              </button>
            </div>
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
      toast.success(`打卡成功：${NODES.find((n) => n[0] === node)?.[1]}`);
    } catch (e) { toast.error(e instanceof Error ? e.message : "打卡失败，请重试或检查网络"); }
    finally { setBusy(false); }
  }

  return (
    <div style={{ background: "#fff", border: "1px solid var(--line-strong)", borderRadius: 14, overflow: "hidden", boxShadow: "0 4px 12px rgba(0,0,0,0.03)" }}>
      {/* 运单状态头 */}
      <div style={{ padding: "16px 18px", borderBottom: "1px dashed var(--line)", background: "rgba(0,0,0,0.01)", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <b style={{ fontSize: 16, letterSpacing: -0.5 }}>{wb.waybill_no}</b>
          <span className="muted" style={{ fontSize: 12 }}>🛣️ {wb.route_name}</span>
        </div>
        <span className="tag" style={{ background: "rgba(39,174,96,0.1)", color: "#27ae60", padding: "4px 8px" }}>运输中</span>
      </div>

      {/* 轨迹上报控制台 */}
      <div style={{ padding: 18 }}>
        <div style={{ fontSize: 12, fontWeight: "bold", color: "var(--ink-2)", marginBottom: 12 }}>📍 时空节点打卡 (同步 AI 调度大屏)</div>
        
        <select 
          value={node} 
          onChange={(e) => setNode(e.target.value)}
          style={{ width: "100%", padding: "12px 14px", fontSize: 15, borderRadius: 10, border: "2px solid var(--line-strong)", background: "#f8fafc", outline: "none", marginBottom: 14 }}
        >
          {NODES.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>

        <div style={{ display: "flex", gap: 12 }}>
          <button 
            style={{ 
              flex: 1, padding: 14, background: "rgba(37,99,235,0.05)", border: "1px solid rgba(37,99,235,0.2)", 
              color: "var(--brand)", borderRadius: 10, fontWeight: "bold", fontSize: 14, display: "flex", alignItems: "center", justifyContent: "center", gap: 6 
            }} 
            disabled={busy} 
            onClick={() => checkin()}
          >
            <span style={{ fontSize: 16, animation: busy ? "pulse 1s infinite" : "none" }}>📡</span> 
            {busy ? "定位中…" : "静默定位"}
          </button>
          
          <label style={{ flex: 1.5 }}>
            <div style={{ 
              width: "100%", padding: 14, background: "var(--grad)", color: "#fff", 
              borderRadius: 10, fontWeight: "bold", fontSize: 14, textAlign: "center", 
              boxShadow: "0 6px 16px rgba(37,99,235,0.3)", cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center", gap: 6 
            }}>
              <span style={{ fontSize: 16 }}>📸</span> {busy ? "上传中…" : "现场拍照打卡"}
            </div>
            <input type="file" accept="image/*" capture="environment" style={{ display: "none" }}
                   onChange={(e) => { const f = e.target.files?.[0]; if (f) checkin(f); e.target.value = ""; }} />
          </label>
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
        <label className="btn-ghost" style={{ cursor: "pointer" }}>
          {busy ? "上传中…" : "选择照片"}
          <input type="file" accept="image/*" capture="environment" style={{ display: "none" }}
                 onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = ""; }} />
        </label>
      </div>
    </div>
  );
}
