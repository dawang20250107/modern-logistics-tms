// 异常闭环：指派 → 处理 → 定责关闭，外加事件时间线。
//
// 这一整段原先**在界面上完全够不着**。后端四个端点都是全的
// （/assign、/handle、/close、/timeline），运单详情页也能"紧急上报"、
// 也能把异常列出来，但没有任何地方能推进它。后果有三层：
//
//   · 上报的异常永远停在「待处理」，订单列表和调度台上那个「⚠ 异常」角标
//     永远不消——一个从不消失的告警，几周之后就没人再看它一眼。
//   · 关闭是**定责的那一步**：责任方、赔付金额都在这里定，金额 > 0 时
//     会落一条应付把异常成本带进对账。够不着 = 异常赔付永远进不了账。
//   · 登录页上写着"异常闭环"，而闭环的后半截没有入口。
//
// 状态机（后端 exceptions/actions.go）：
//   pending_handle --指派--> handling --处理--> pending_audit --关闭--> closed
// 每一步只给出当前状态该做的那一个动作，不把四个按钮一起摆出来——
// 摆出来就要解释为什么点了没反应。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import { hasPerm, useAuth } from "../auth/auth";
import { confirmAction } from "../api/confirm";
import { EMPTY as DASH, fmtDateTime, fmtMoney } from "../api/format";
import { toast } from "../api/toast";
import type { Employee, ExceptionEvent, ExceptionRecord, Paginated } from "../api/types";
import { EXC_EVENT_LABEL } from "../api/types";

const STATUS_LABEL: Record<string, string> = {
  pending_handle: "待处理", handling: "处理中", pending_audit: "待审核",
  closed: "已关闭", rejected: "已驳回",
};
const TYPE_LABEL: Record<string, string> = {
  transit_delay: "在途超时", route_deviation: "偏航", cargo_damage: "货损货差",
  vehicle_breakdown: "车辆故障", detained: "扣车扣货", customer_complaint: "客户投诉", other: "其他",
};
const LEVEL_LABEL: Record<string, string> = { high: "高", medium: "中", low: "低", none: "无" };
// 责任方是自由文本（varchar 80），这里给一组固定选项让它可统计；
// 词表与费用的收付款方对齐，多一个"我方"和"不定责"。
const PARTY: Array<[string, string]> = [
  ["carrier", "承运商"], ["driver", "司机"], ["customer", "客户"],
  ["self", "我方"], ["third_party", "第三方"], ["none", "不定责"],
];
const PARTY_LABEL = Object.fromEntries(PARTY);

export function ExceptionCloseLoop({ items, onChanged }: {
  items: ExceptionRecord[];
  onChanged: () => void;
}) {
  const [open, setOpen] = useState<string | null>(null);
  if (items.length === 0) return null;
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th>类型</th><th>级别</th><th>描述</th><th>状态</th>
            <th>责任方</th><th className="num">赔付</th><th>处理</th>
          </tr>
        </thead>
        <tbody>
          {items.map((ex) => (
            <ExceptionRow
              key={ex.id} ex={ex}
              expanded={open === ex.id}
              onToggle={() => setOpen(open === ex.id ? null : ex.id)}
              onChanged={onChanged}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ExceptionRow({ ex, expanded, onToggle, onChanged }: {
  ex: ExceptionRecord; expanded: boolean; onToggle: () => void; onChanged: () => void;
}) {
  const queryClient = useQueryClient();
  const [assignee, setAssignee] = useState(ex.assignee ?? "");
  const [resolution, setResolution] = useState(ex.resolution ?? "");
  const [party, setParty] = useState(ex.responsibility_party || "carrier");
  const [amount, setAmount] = useState(String(ex.amount ?? "0"));

  const employees = useQuery({
    queryKey: ["employees-for-exception"],
    queryFn: () => apiGet<Paginated<Employee>>("/org/employees?page_size=200"),
    enabled: expanded && ex.status === "pending_handle",
  });
  const timeline = useQuery({
    queryKey: ["exception", ex.id, "timeline"],
    queryFn: () => apiGet<ExceptionEvent[]>(`/exceptions/${ex.id}/timeline`),
    enabled: expanded,
  });

  const done = (msg: string) => {
    toast.success(msg);
    queryClient.invalidateQueries({ queryKey: ["exception", ex.id, "timeline"] });
    onChanged();
  };
  // 指派、提交处理结论、定责闭环三个动作都要 waybill.manage
  // （上报异常刻意只要 waybill.view——发现问题的常常是客服，
  // 登记的门要低；而定责那一步会落一条应付，门要高）。
  // 这个面板嵌在调度台和运单详情里，只读角色也看得到它。
  const { user } = useAuth();
  const canManage = hasPerm(user, "waybill.manage");

  const assign = useMutation({
    mutationFn: () => apiPost(`/exceptions/${ex.id}/assign`, { assignee }),
    onSuccess: () => done("已指派，异常进入处理中"),
  });
  const handle = useMutation({
    mutationFn: () => apiPost(`/exceptions/${ex.id}/handle`, { resolution }),
    onSuccess: () => done("已提交处理结论，等待审核"),
  });
  const close = useMutation({
    mutationFn: () => apiPost(`/exceptions/${ex.id}/close`, {
      responsibility_party: party, amount, resolution,
    }),
    onSuccess: () => done("异常已闭环"),
  });

  const amt = Number(amount) || 0;
  return (
    <>
      <tr onClick={onToggle} style={{ cursor: "pointer" }} title={expanded ? "收起" : "展开处理"}>
        <td>{TYPE_LABEL[ex.exception_type] ?? ex.exception_type}</td>
        <td>
          <span className={`tag tag-${ex.level === "high" ? "high" : ex.level === "low" ? "low" : "medium"}`}>
            {LEVEL_LABEL[ex.level] ?? ex.level}
          </span>
        </td>
        <td className="small">{ex.description || DASH}</td>
        <td>{STATUS_LABEL[ex.status] ?? ex.status}</td>
        <td>{ex.responsibility_party ? (PARTY_LABEL[ex.responsibility_party] ?? ex.responsibility_party) : DASH}</td>
        <td className="num">{Number(ex.amount) > 0 ? fmtMoney(ex.amount) : DASH}</td>
        <td className="small link">{expanded ? "收起" : ex.status === "closed" ? "查看" : "处理"}</td>
      </tr>
      {expanded && (
        <tr>
          <td colSpan={7} style={{ background: "var(--panel-2)" }}>
            <div className="stack-sm" style={{ padding: "10px 4px" }}>
              {ex.status === "pending_handle" && (
                <div className="form-row" style={{ gap: 8, flexWrap: "wrap", padding: 0 }}>
                  <select value={assignee} onChange={(e) => setAssignee(e.target.value)}>
                    <option value="">选择处理人…</option>
                    {(employees.data?.items ?? []).map((m) => (
                      <option key={m.id} value={m.user ?? ""}>{m.name}{m.position ? `（${m.position}）` : ""}</option>
                    ))}
                  </select>
                  {canManage
                    ? <><button className="btn-primary" disabled={!assignee || assign.isPending}
                              onClick={() => assign.mutate()}>指派</button>
                      <span className="muted small">指派后异常进入「处理中」。</span></>
                    : <span className="muted small" title="需要 waybill.manage 权限点">只读账号：可查看进展，不能指派或定责</span>}
                </div>
              )}

              {ex.status === "handling" && (
                <div className="stack-sm">
                  <textarea className="search" style={{ width: "100%", minHeight: 60 }}
                            placeholder="处理结论：做了什么、现场如何、后续怎么办"
                            value={resolution} onChange={(e) => setResolution(e.target.value)} />
                  <div className="form-row" style={{ gap: 8, padding: 0 }}>
                    {canManage && <><button className="btn-primary" disabled={!resolution.trim() || handle.isPending}
                            onClick={() => handle.mutate()}>提交处理结论</button>
                    <span className="muted small">提交后进入「待审核」，由能定责的人关闭。</span></>}
                  </div>
                </div>
              )}

              {ex.status === "pending_audit" && (
                <div className="stack-sm">
                  <div className="form-row" style={{ gap: 8, flexWrap: "wrap", padding: 0 }}>
                    <label className="small">责任方
                      <select value={party} onChange={(e) => setParty(e.target.value)}>
                        {PARTY.map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                      </select>
                    </label>
                    <label className="small">赔付金额(元)
                      <input className="search" style={{ width: 110 }} inputMode="decimal"
                             value={amount} onChange={(e) => setAmount(e.target.value)} />
                    </label>
                  </div>
                  <textarea className="search" style={{ width: "100%", minHeight: 56 }}
                            placeholder="定责结论（会写进事件记录，结算时要拿出来）"
                            value={resolution} onChange={(e) => setResolution(e.target.value)} />
                  {/* 关闭会落一条应付，而且关过就不能再关（后端 409）。
                      这句提示不是客套：金额一旦落进对账，改起来要走反向流程。 */}
                  <div className="muted small">
                    {amt > 0
                      ? `关闭会按 ${fmtMoney(amount)} 生成一条「异常费用」应付，计入本单成本与对账。`
                      : "赔付金额为 0 时只闭环、不生成费用。"}
                    　关闭后不能再次关闭，更正需重新打开异常。
                  </div>
                  <div className="form-row" style={{ gap: 8, padding: 0 }}>
                    {canManage && <button className="btn-danger" disabled={close.isPending}
                            onClick={async () => {
                              const ok = await confirmAction({
                                title: "确认闭环这条异常？",
                                message: amt > 0
                                  ? `责任方「${PARTY_LABEL[party]}」，赔付 ${fmtMoney(amount)}，将生成一条应付并计入对账。`
                                  : `责任方「${PARTY_LABEL[party]}」，不产生赔付。`,
                                confirmText: "确认闭环",
                              });
                              if (ok) close.mutate();
                            }}>定责并闭环</button>}
                  </div>
                </div>
              )}

              {ex.status === "closed" && (
                <div className="muted small">
                  已闭环：责任方 {PARTY_LABEL[ex.responsibility_party] ?? ex.responsibility_party ?? DASH}
                  ，赔付 {Number(ex.amount) > 0 ? fmtMoney(ex.amount) : "0"}
                  {ex.resolution ? `　结论：${ex.resolution}` : ""}
                </div>
              )}

              {/* 时间线：谁在什么时候做了什么。定责有争议时要拿它说话。 */}
              <div className="stack-sm">
                <b className="small">处理记录</b>
                {timeline.isLoading && <span className="muted small">读取中…</span>}
                {!timeline.isLoading && (timeline.data?.length ?? 0) === 0 && (
                  <span className="muted small">暂无记录。</span>
                )}
                {(timeline.data ?? []).map((e) => (
                  <div key={e.id} className="small" style={{ display: "flex", gap: 10 }}>
                    <span className="mono muted" style={{ minWidth: 130 }}>{fmtDateTime(e.event_time)}</span>
                    <span style={{ minWidth: 44 }}>{EXC_EVENT_LABEL[e.event_type] ?? e.event_type}</span>
                    <span className="muted" style={{ minWidth: 90 }}>{e.actor_name || DASH}</span>
                    <span>{e.note || DASH}</span>
                  </div>
                ))}
              </div>
            </div>
          </td>
        </tr>
      )}
    </>
  );
}
