import { useMutation } from "@tanstack/react-query";
import { useRef, useState } from "react";

import { apiPost } from "../api/client";
import { toast } from "../api/toast";
import { useModalA11y } from "../api/useModalA11y";
import type { Order } from "../api/types";

// 人工登记异常类型（与后端 EXCEPTION_TYPE_CHOICES 人工部分对齐）
const EXC_TYPES: [string, string][] = [
  ["transit_delay", "在途超时"], ["route_deviation", "偏航/路线异常"], ["cargo_damage", "货损货差"],
  ["vehicle_breakdown", "车辆故障"], ["detained", "扣车扣货"], ["customer_complaint", "客户投诉"],
  ["temperature", "冷链温度异常"], ["abnormal_stop", "异常停车"], ["other", "其他"],
];
const EXC_LEVELS: [string, string][] = [["high", "高"], ["medium", "中"], ["low", "低"]];

// 在订单池对某订单登记异常：挂到订单，同步调度与订单管理，并给订单打标记。
export function ExceptionRegisterModal({
  order, onClose, onDone,
}: {
  order: Order;
  onClose: () => void;
  onDone: () => void;
}) {
  const [excType, setExcType] = useState("transit_delay");
  const [level, setLevel] = useState("medium");
  const [desc, setDesc] = useState("");
  const cardRef = useRef<HTMLDivElement>(null);
  useModalA11y(true, cardRef, onClose);

  const submit = useMutation({
    mutationFn: () => apiPost(`/orders/${order.id}/report-exception`, {
      exception_type: excType, level, description: desc,
    }),
    onSuccess: () => { toast.success(`已登记异常，已同步调度与订单管理`); onDone(); },
    onError: (e: Error) => toast.error(e.message || "登记失败"),
  });

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div ref={cardRef} className="modal-card" style={{ width: "min(460px, 94vw)" }} onClick={(e) => e.stopPropagation()} tabIndex={-1}>
        <div className="bd-head">
          <div>
            <div className="bd-title">登记异常</div>
            <div className="muted small" style={{ marginTop: 3 }}>
              <span className="mono">{order.order_no}</span> · {order.customer_name || "散客"} · {order.origin} → {order.destination}
            </div>
          </div>
          <button className="btn-ghost" onClick={onClose}>关闭 [Esc]</button>
        </div>
        <div className="bd-body">
          <div className="bd-row2">
            <div className="bd-field">
              <label>异常类型</label>
              <select className="search" value={excType} onChange={(e) => setExcType(e.target.value)}>
                {EXC_TYPES.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </div>
            <div className="bd-field">
              <label>紧急程度</label>
              <select className="search" value={level} onChange={(e) => setLevel(e.target.value)}>
                {EXC_LEVELS.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </div>
          </div>
          <div className="bd-field">
            <label>情况描述</label>
            <textarea
              autoFocus
              value={desc} onChange={(e) => setDesc(e.target.value)} rows={4}
              placeholder="时间、地点、货物、责任方等（登记后订单打标，调度/订单管理同步可见）"
              style={{ padding: "8px 10px", border: "1px solid var(--line-2)", borderRadius: "var(--radius-sm)", fontSize: 13, resize: "vertical" }}
            />
          </div>
        </div>
        <div className="bd-foot">
          <span className="muted small">登记后订单打「异常」标记，同步至调度与订单管理台账</span>
          <div style={{ flex: 1 }} />
          <button className="btn-ghost" onClick={onClose}>取消</button>
          <button className="btn-primary" disabled={!desc.trim() || submit.isPending} onClick={() => submit.mutate()}>
            {submit.isPending ? "登记中…" : "登记异常"}
          </button>
        </div>
      </div>
    </div>
  );
}
