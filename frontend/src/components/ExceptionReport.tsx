import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { fmtRelative } from "../api/format";
import { toast } from "../api/toast";
import type { ExceptionRecord, Paginated } from "../api/types";
import { StateView } from "./StateView";

const STATUS_LABEL: Record<string, string> = {
  pending_handle: "待处理", handling: "处理中", pending_audit: "待审核", closed: "已关闭", rejected: "已驳回",
};
const EXC_TYPE_LABEL: Record<string, string> = {
  transit_delay: "在途超时", route_deviation: "偏航/路线异常", cargo_damage: "货损货差",
  vehicle_breakdown: "车辆故障", detained: "扣车扣货", customer_complaint: "客户投诉",
  receipt_pending: "回单待回收", receipt_exception: "回单异常", other: "其他",
};
// 客服侧常用提报类型（偏客户诉求/回单）
const CS_TYPES = ["customer_complaint", "receipt_exception", "cargo_damage", "transit_delay", "detained", "other"];

// 异常提报（客服工作台）：客服受理客户诉求/回单异常，一键立案，交调度处置。
export function ExceptionReport() {
  const queryClient = useQueryClient();
  const [type, setType] = useState("customer_complaint");
  const [level, setLevel] = useState("medium");
  const [desc, setDesc] = useState("");

  const list = useQuery({
    queryKey: ["exceptions", "recent"],
    queryFn: () => apiGet<Paginated<ExceptionRecord>>("/exceptions?page_size=8"),
  });

  const create = useMutation({
    mutationFn: () => apiPost("/exceptions", { exception_type: type, description: desc, level }),
    onSuccess: () => {
      setDesc("");
      toast.success("异常已立案，将由调度跟进处置");
      queryClient.invalidateQueries({ queryKey: ["exceptions"] });
    },
  });

  const items = list.data?.items ?? [];

  return (
    <div className="panel">
      <div className="panel-head">异常提报</div>
      <div className="grid-form" style={{ padding: "14px 18px", gridTemplateColumns: "160px 130px 1fr auto", alignItems: "end", gap: 12 }}>
        <label>异常类型
          <select value={type} onChange={(e) => setType(e.target.value)}>
            {CS_TYPES.map((k) => <option key={k} value={k}>{EXC_TYPE_LABEL[k] ?? k}</option>)}
          </select>
        </label>
        <label>紧急程度
          <select value={level} onChange={(e) => setLevel(e.target.value)}>
            <option value="high">高风险</option><option value="medium">中风险</option><option value="low">低风险</option>
          </select>
        </label>
        <label>情况描述
          <input placeholder="客户诉求 / 时间 / 单号 / 责任方等" value={desc} onChange={(e) => setDesc(e.target.value)} />
        </label>
        <button className="btn-primary" disabled={create.isPending || !desc.trim()} onClick={() => create.mutate()}>
          {create.isPending ? "提报中…" : "立案提报"}
        </button>
      </div>

      <div className="section-label" style={{ padding: "4px 18px 0" }}>最近异常</div>
      {list.isLoading ? (
        <StateView kind="loading" compact />
      ) : items.length === 0 ? (
        <div className="muted small" style={{ padding: "10px 18px 16px" }}>暂无异常记录。</div>
      ) : (
        <table className="table" style={{ fontSize: 12.5 }}>
          <tbody>
            {items.map((e) => (
              <tr key={e.id}>
                <td style={{ fontWeight: 600 }}>{EXC_TYPE_LABEL[e.exception_type] ?? e.exception_type}</td>
                <td className="mono small">{e.waybill_no || "全局"}</td>
                <td><span className={`tag tag-${e.status === "closed" ? "low" : "medium"}`}>{STATUS_LABEL[e.status] ?? e.status}</span></td>
                <td className="muted small" style={{ textAlign: "right" }}>{fmtRelative(e.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
