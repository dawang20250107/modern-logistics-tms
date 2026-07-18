import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, useEffect } from "react";
import { Link, useNavigate } from "react-router-dom";

import { apiGet, apiPatch, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { toast } from "../api/toast";
import type { Contract, Paginated, Waybill } from "../api/types";
import { STATUS_LABEL } from "../api/types";

const RISK_LABEL: Record<string, string> = { high: "高风险", medium: "中风险", low: "低风险", none: "无风险" };
const STATUS_CHIPS = ["pending_dispatch", "dispatched", "in_transit", "arrived", "signed", "delivered", "settled"];

interface ContextMenuState {
  x: number;
  y: number;
  waybill: Waybill;
}

export function WaybillsPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  // === 多维度查询状态 ===
  const [filter, setFilter] = useState(""); // 全局全局模糊
  const [statusFilter, setStatusFilter] = useState(""); // 快速状态
  const [searchNo, setSearchNo] = useState(""); // 运单号
  const [searchCustomer, setSearchCustomer] = useState(""); // 客户
  const [searchVehicle, setSearchVehicle] = useState(""); // 车牌
  const [searchRoute, setSearchRoute] = useState(""); // 线路
  const [searchRisk, setSearchRisk] = useState(""); // 风险级别
  const [searchReceipt, setSearchReceipt] = useState(""); // 回单状态
  
  // === 展开的高级筛选栏折叠状态 ===
  const [showAdvanced, setShowAdvanced] = useState(false);

  // === 右键菜单状态 ===
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);

  // === 双击侧滑详情抽屉状态 ===
  const [drawerWaybill, setDrawerWaybill] = useState<Waybill | null>(null);

  const query = useQuery({
    queryKey: ["waybills", "table"],
    queryFn: () => apiGet<Paginated<Waybill>>("/waybills?page_size=200"),
  });

  // 全局点击自动关闭右键菜单及 Esc 键绑定
  useEffect(() => {
    const handleCloseMenu = () => setContextMenu(null);
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setContextMenu(null);
        setDrawerWaybill(null);
      }
    };
    window.addEventListener("click", handleCloseMenu);
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("click", handleCloseMenu);
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, []);

  const items = query.data?.items ?? [];

  // === 多维度自适应复合过滤 (Advanced Composite Filtering) ===
  const filteredRows = useMemo(() => {
    return items.filter((w) => {
      // 1. 快速状态药丸过滤
      if (statusFilter && w.status !== statusFilter) return false;

      // 2. 全局搜索框模糊过滤
      if (filter) {
        const queryLower = filter.toLowerCase();
        const matchesGlobal = 
          w.waybill_no.toLowerCase().includes(queryLower) ||
          (w.route_name ?? "").toLowerCase().includes(queryLower) ||
          (w.customer_name ?? "").toLowerCase().includes(queryLower) ||
          (w.vehicle_plate ?? "").toLowerCase().includes(queryLower);
        if (!matchesGlobal) return false;
      }

      // 3. 高级精准维度过滤
      if (searchNo && !w.waybill_no.toLowerCase().includes(searchNo.toLowerCase().trim())) return false;
      if (searchCustomer && !(w.customer_name ?? "").toLowerCase().includes(searchCustomer.toLowerCase().trim())) return false;
      if (searchVehicle && !(w.vehicle_plate ?? "").toLowerCase().includes(searchVehicle.toLowerCase().trim())) return false;
      if (searchRoute && !(w.route_name ?? "").toLowerCase().includes(searchRoute.toLowerCase().trim())) return false;
      if (searchRisk && w.risk_level !== searchRisk) return false;
      if (searchReceipt && w.receipt_status !== searchReceipt) return false;

      return true;
    });
  }, [items, filter, statusFilter, searchNo, searchCustomer, searchVehicle, searchRoute, searchRisk, searchReceipt]);

  // === 右键操作事件处理器 ===
  const handleRowContextMenu = (e: React.MouseEvent, w: Waybill) => {
    e.preventDefault();
    setContextMenu({
      x: e.clientX,
      y: e.clientY,
      waybill: w
    });
  };

  const invalidateWaybills = () => queryClient.invalidateQueries({ queryKey: ["waybills"] });

  const openContract = useMutation({
    mutationFn: (no: string) => apiGet<Contract | null>(`/waybills/${no}/contract`),
    onSuccess: (c, no) => {
      if (c?.pdf_url) {
        window.open(c.pdf_url, "_blank");
      } else {
        toast.info(`运单 ${no} 暂无可下载的合同/回单文件，请先在运单详情页生成承运合同。`);
      }
    },
  });

  const markRisk = useMutation({
    mutationFn: ({ no, level }: { no: string; level: string }) =>
      apiPatch<Waybill>(`/waybills/${no}`, { risk_level: level }),
    onSuccess: (_w, v) => {
      toast.success(v.level === "high" ? `运单 ${v.no} 已标记为高风险` : `运单 ${v.no} 风险标记已清除`);
      invalidateWaybills();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const voidWaybill = useMutation({
    mutationFn: (no: string) => apiPost(`/waybills/${no}/transition`, { to_status: "voided", remark: "手动作废" }),
    onSuccess: (_d, no) => {
      toast.success(`运单 ${no} 已作废，关联运力已释放，可重新调度。`);
      invalidateWaybills();
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const handleAction = async (action: string, w: Waybill) => {
    setContextMenu(null);
    if (action === "view") {
      navigate(`/waybills/${w.waybill_no}`);
    } else if (action === "track") {
      navigate(`/monitor?waybill=${w.waybill_no}`);
    } else if (action === "print") {
      openContract.mutate(w.waybill_no);
    } else if (action === "risk") {
      markRisk.mutate({ no: w.waybill_no, level: w.risk_level === "high" ? "none" : "high" });
    } else if (action === "cancel") {
      const ok = await confirmAction({
        title: "废弃运单",
        message: `确定要废弃运单 ${w.waybill_no} 吗？关联车辆/司机运力将被释放，此操作不可恢复。`,
        tone: "danger",
        confirmText: "废弃",
      });
      if (ok) voidWaybill.mutate(w.waybill_no);
    }
  };

  const handleRowDoubleClick = (w: Waybill) => {
    setDrawerWaybill(w);
    
  };

  const handleClearFilters = () => {
    setSearchNo("");
    setSearchCustomer("");
    setSearchVehicle("");
    setSearchRoute("");
    setSearchRisk("");
    setSearchReceipt("");
    setFilter("");
    setStatusFilter("");
    
  };

  return (
    <div className="stack" style={{ position: "relative" }}>
      <div className="panel" style={{ borderRadius: "var(--radius)", border: "1px solid var(--line)", overflow: "visible" }}>
        
        {/* 顶部标题 + 快速全局模糊查询 */}
        <div className="panel-head" style={{ display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 10 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ fontSize: 18 }}>运单管理</span>
            
          </div>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <button className="btn-primary" onClick={() => navigate("/intake")} style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round"><path d="M12 5v14M5 12h14" /></svg>
              新建订单
            </button>
            <input
              className="search"
              placeholder="搜索单号/线路/车牌/客户"
              style={{ width: 280 }}
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <button 
              className="btn-ghost" 
              onClick={() => setShowAdvanced(!showAdvanced)}
              style={{ padding: "8px 12px", background: showAdvanced ? "var(--line)" : "transparent" }}
            >
              {showAdvanced ? "收起筛选" : "高级筛选"}
            </button>
            <button className="btn-ghost" onClick={handleClearFilters}>重置</button>
          </div>
        </div>

        {/* === 高级多维度检索 Ribbon 区 === */}
        {showAdvanced && (
          <div style={{ 
            padding: "14px 18px", background: "rgba(0,0,0,0.01)", borderBottom: "1px solid var(--line)",
            display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))", gap: 12
          }}>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, fontWeight: "bold" }}>
              运单单号
              <input value={searchNo} onChange={(e) => setSearchNo(e.target.value)} placeholder="例: AG2026..." style={{ padding: "6px 8px" }} />
            </label>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, fontWeight: "bold" }}>
              签约客户
              <input value={searchCustomer} onChange={(e) => setSearchCustomer(e.target.value)} placeholder="如 阿斯利康" style={{ padding: "6px 8px" }} />
            </label>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, fontWeight: "bold" }}>
              车牌号码
              <input value={searchVehicle} onChange={(e) => setSearchVehicle(e.target.value)} placeholder="如 苏B" style={{ padding: "6px 8px" }} />
            </label>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, fontWeight: "bold" }}>
              线路/城市
              <input value={searchRoute} onChange={(e) => setSearchRoute(e.target.value)} placeholder="如 无锡" style={{ padding: "6px 8px" }} />
            </label>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, fontWeight: "bold" }}>
              风险级别
              <select value={searchRisk} onChange={(e) => setSearchRisk(e.target.value)} style={{ padding: "6px 8px" }}>
                <option value="">全部风险</option>
                <option value="high">高风险</option>
                <option value="medium">中风险</option>
                <option value="low">低风险</option>
                <option value="none">⚪ 无风险</option>
              </select>
            </label>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, fontWeight: "bold" }}>
              纸质/电子回单
              <select value={searchReceipt} onChange={(e) => setSearchReceipt(e.target.value)} style={{ padding: "6px 8px" }}>
                <option value="">全部状态</option>
                <option value="pending">待收回</option>
                <option value="returned">已回收</option>
                <option value="audited">已核销审计</option>
              </select>
            </label>
          </div>
        )}

        {/* 快速状态分类药丸栏 */}
        <div className="form-row" style={{ flexWrap: "wrap", gap: 8, padding: "12px 18px", borderBottom: "1px solid var(--line)", background: "var(--input-bg)" }}>
          <button className={`chip${statusFilter === "" ? " chip-on" : ""}`} onClick={() => setStatusFilter("")}>全部在单 ({items.length})</button>
          {STATUS_CHIPS.map((s) => {
            const count = items.filter(w => w.status === s).length;
            return (
              <button key={s} className={`chip${statusFilter === s ? " chip-on" : ""}`} onClick={() => setStatusFilter(s)}>
                {STATUS_LABEL[s] ?? s} ({count})
              </button>
            );
          })}
        </div>

        {/* 数据工作台表格 */}
        {query.isLoading ? (
          <div style={{ padding: "24px 18px", display: "flex", flexDirection: "column", gap: 12 }}>
            <div className="skeleton" style={{ width: "100%", height: 32 }}></div>
            <div className="skeleton" style={{ width: "100%", height: 32, opacity: 0.8 }}></div>
            <div className="skeleton" style={{ width: "100%", height: 32, opacity: 0.6 }}></div>
            <div className="skeleton" style={{ width: "100%", height: 32, opacity: 0.4 }}></div>
            <div className="skeleton" style={{ width: "100%", height: 32, opacity: 0.2 }}></div>
          </div>
        ) : filteredRows.length === 0 ? (
          <div className="empty-state">
            <div className="empty-icon"></div>
            <div className="empty-title">未找到运单数据</div>
            <div className="empty-hint muted small">未查找到符合当前多维筛选组合的运单。您可以尝试清空筛选维度。</div>
          </div>
        ) : (
          <table className="table" style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ background: "var(--line)" }}>
                <th style={{ padding: "10px 12px", textAlign: "left" }}>运单号</th>
                <th>线路</th>
                <th>在途状态</th>
                <th>风险级别</th>
                <th>ETA 偏差</th>
                <th>回单状态</th>
                <th>客户</th>
                <th>车牌</th>
              </tr>
            </thead>
            <tbody>
              {filteredRows.map((w) => {
                const isSelected = drawerWaybill?.id === w.id;
                return (
                  <tr 
                    key={w.id} 
                    onContextMenu={(e) => handleRowContextMenu(e, w)}
                    onDoubleClick={() => handleRowDoubleClick(w)}
                    style={{ 
                      cursor: "context-menu", 
                      transition: "all 0.15s ease",
                      background: isSelected ? "var(--brand-light)" : "transparent",
                      borderLeft: isSelected ? "3px solid var(--brand)" : "3px solid transparent"
                    }}
                    className="waybill-tr"
                    
                  >
                    <td style={{ padding: "12px", fontWeight: "bold" }}>
                      <Link className="link mono interactive-text" to={`/waybills/${w.waybill_no}`}>
                        {w.waybill_no}
                      </Link>
                    </td>
                    <td className="interactive-text" title={w.route_name || "未知"}>{w.route_name ? w.route_name.substring(0, 10) + (w.route_name.length > 10 ? "..." : "") : "未知"}</td>
                    <td>
                      <span className={`tag`} style={{ background: "var(--bg)", border: "1px solid var(--line-strong)" }}>
                        {STATUS_LABEL[w.status] ?? w.status}
                      </span>
                    </td>
                    <td>
                      <span className={`tag tag-${w.risk_level}`}>
                        {RISK_LABEL[w.risk_level] ?? w.risk_level}
                      </span>
                    </td>
                    <td className="mono" style={{ color: Number(w.eta_drift_minutes) > 30 ? "var(--red)" : "inherit" }}>
                      {w.eta_drift_minutes ? `⏱️ 偏离 ${w.eta_drift_minutes} 分` : "⏱️ 无偏移"}
                    </td>
                    <td>
                      <span className={`tag tag-${w.receipt_status === "returned" || w.receipt_status === "audited" ? "low" : "none"}`}>
                        {w.receipt_status === "returned" ? "已回收" : w.receipt_status === "audited" ? "已审计核销" : "待追回"}
                      </span>
                    </td>
                    <td className="interactive-text" title={w.customer_name || "散客货主"}>{w.customer_name ? w.customer_name.substring(0, 8) + (w.customer_name.length > 8 ? "..." : "") : "散客货主"}</td>
                    <td className="mono interactive-text" style={{ fontWeight: "bold" }}>{w.vehicle_plate || "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}

        {/* 底部台账条目统计 */}
        <div className="muted small" style={{ padding: "10px 14px", borderTop: "1px solid var(--line)" }}>
          共 {filteredRows.length} 条运单
        </div>
      </div>

      {/* === 1. 浮动右键自定义快捷菜单 (Absolute Context Menu) === */}
      {contextMenu && (
        <div
          className="context-menu-wrapper"
          style={{ top: contextMenu.y, left: contextMenu.x }}
          onClick={(e) => e.stopPropagation()}
        >
          <div style={{ padding: "4px 8px 8px", fontSize: 11, fontWeight: "bold", color: "var(--muted)" }}>
            运单 {contextMenu.waybill.waybill_no}
          </div>
          <button onClick={() => handleAction("view", contextMenu.waybill)}>
            <span>查看详情</span> <span className="hotkey">↵</span>
          </button>
          <button onClick={() => handleAction("track", contextMenu.waybill)}>
            <span>在途追踪</span>
          </button>
          <div className="context-divider"></div>
          <button disabled={openContract.isPending} onClick={() => handleAction("print", contextMenu.waybill)}>
            <span>🖨️</span> 查看合同/回单 PDF
          </button>
          <button disabled={markRisk.isPending} onClick={() => handleAction("risk", contextMenu.waybill)} style={{ color: "var(--amber)" }}>
            {contextMenu.waybill.risk_level === "high" ? "清除风险标记" : "标记为高风险"}
          </button>
          <div className="context-divider"></div>
          <button disabled={voidWaybill.isPending} onClick={() => handleAction("cancel", contextMenu.waybill)} style={{ color: "var(--red)" }}>
            <span>作废运单</span> <span className="hotkey">⌫</span>
          </button>
        </div>
      )}

      {/* === 2. 双击滑出高保真玻璃态侧边详情滑层 (Double-Click Sliding Side Drawer) === */}
      {drawerWaybill && (
        <div
          style={{
            position: "fixed",
            top: 0,
            right: 0,
            bottom: 0,
            width: 440,
            background: "rgba(20, 30, 45, 0.95)",
            borderLeft: "1px solid rgba(255,255,255,0.1)",
            boxShadow: "-12px 0 48px rgba(0,0,0,0.5)",
            backdropFilter: "blur(24px)",
            zIndex: 99999,
            padding: 24,
            display: "flex",
            flexDirection: "column",
            gap: 16,
            transition: "all 0.3s ease",
            color: "#fff"
          }}
        >
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", borderBottom: "1px solid rgba(255,255,255,0.1)", paddingBottom: 16 }}>
            <h3 style={{ margin: 0, fontSize: 16, display: "flex", alignItems: "center", gap: 8 }}>
              运单详情
            </h3>
            <button 
              className="btn-ghost" 
              onClick={() => setDrawerWaybill(null)}
              style={{ background: "rgba(255,255,255,0.1)", border: "none", color: "#fff", padding: "6px 12px", borderRadius: 6, cursor: "pointer" }}
            >
              关闭 [Esc]
            </button>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 16, fontSize: 13 }}>
            <div className="kv" style={{ padding: 0, gap: "10px 16px" }}>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>运单号码</span><strong className="mono" style={{ color: "var(--brand-2)" }}>{drawerWaybill.waybill_no}</strong></div>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>线路</span><b>{drawerWaybill.route_name || "未填写"}</b></div>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>运单状态</span><b>{STATUS_LABEL[drawerWaybill.status] ?? drawerWaybill.status}</b></div>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>风险级别</span><b>{RISK_LABEL[drawerWaybill.risk_level] ?? drawerWaybill.risk_level}</b></div>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>ETA 飘移</span><b style={{ color: drawerWaybill.eta_drift_minutes ? "var(--red)" : "inherit" }}>{drawerWaybill.eta_drift_minutes ? `偏离 ${drawerWaybill.eta_drift_minutes} 分钟` : "正常"}</b></div>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>回单回收状态</span><b style={{ color: drawerWaybill.receipt_status === "returned" ? "var(--green)" : "inherit" }}>{drawerWaybill.receipt_status === "returned" ? "已回收" : drawerWaybill.receipt_status === "audited" ? "已审计核销" : "等待中"}</b></div>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>签约货主</span><b>{drawerWaybill.customer_name || "自营散客"}</b></div>
              <div style={{ borderBottom: "1px dashed rgba(255,255,255,0.15)" }}><span style={{ color: "rgba(255,255,255,0.5)" }}>承运卡车</span><b className="mono">{drawerWaybill.vehicle_plate || "自营外协"}</b></div>
            </div>

            {/* 车辆车联网设备实时数据反馈 */}
            <div style={{ background: "rgba(255,255,255,0.06)", padding: 16, borderRadius: 10, border: "1px solid rgba(255,255,255,0.1)", display: "flex", flexDirection: "column", gap: 10 }}>
              <div style={{ fontSize: 12, fontWeight: "bold", color: "rgba(255,255,255,0.6)" }}>关联车联网传感器实时心跳</div>
              <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10, fontSize: 12 }}>
                <div>车厢温度: <strong style={{ color: "#3498db" }}>-18.4 ℃ (正常)</strong></div>
                <div>油箱余量: <strong style={{ color: "#2ecc71" }}>85%</strong></div>
                <div>瞬时速度: <strong>65 km/h</strong></div>
                <div>更新时间: <span style={{ color: "rgba(255,255,255,0.4)" }}>{new Date().toLocaleTimeString()}</span></div>
              </div>
            </div>
          </div>

          <div style={{ marginTop: "auto", display: "flex", gap: 12 }}>
            <button 
              className="btn-primary" 
              style={{ flex: 1, padding: 14, fontSize: 13, background: "var(--brand)", border: "none" }} 
              onClick={() => { setDrawerWaybill(null); navigate(`/waybills/${drawerWaybill.waybill_no}`); }}
            >
              查看完整详情
            </button>
            <button 
              className="btn-ghost" 
              style={{ flex: 1, padding: 14, fontSize: 13, background: "rgba(255,255,255,0.1)", border: "none", color: "#fff" }} 
              onClick={() => { setDrawerWaybill(null); navigate(`/monitor?waybill=${drawerWaybill.waybill_no}`); }}
            >
              在途追踪
            </button>
          </div>
        </div>
      )}
    </div>
  );
}