import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiGet, apiPost } from "../api/client";
import type { ExceptionRecord, Paginated } from "../api/types";
import { useAuth } from "../auth/auth";

const LEVEL_LABEL: Record<string, string> = { low: "低", medium: "中", high: "高" };
const STATUS_LABEL: Record<string, string> = {
  pending_handle: "待处理",
  handling: "处理中",
  pending_audit: "待审核",
  closed: "已关闭",
  rejected: "已驳回",
};

export function ExceptionsPage() {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [type, setType] = useState("");
  const [desc, setDesc] = useState("");
  const [level, setLevel] = useState("medium");

  const list = useQuery({
    queryKey: ["exceptions"],
    queryFn: () => apiGet<Paginated<ExceptionRecord>>("/exceptions?page_size=100"),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["exceptions"] });

  const create = useMutation({
    mutationFn: () => apiPost("/exceptions", { exception_type: type || "manual", description: desc, level }),
    onSuccess: () => {
      setType("");
      setDesc("");
      invalidate();
    },
  });
  const act = useMutation({
    mutationFn: (v: { id: string; action: string; body?: Record<string, unknown> }) =>
      apiPost(`/exceptions/${v.id}/${v.action}`, v.body ?? {}),
    onSuccess: invalidate,
  });

  const items = list.data?.items ?? [];

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">登记异常</div>
        <div className="form-row">
          <input placeholder="异常类型" value={type} onChange={(e) => setType(e.target.value)} />
          <select value={level} onChange={(e) => setLevel(e.target.value)}>
            <option value="low">低</option>
            <option value="medium">中</option>
            <option value="high">高</option>
          </select>
          <input placeholder="描述" value={desc} onChange={(e) => setDesc(e.target.value)} style={{ flex: 1 }} />
          <button className="btn-primary" disabled={create.isPending} onClick={() => create.mutate()}>
            登记
          </button>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">异常列表</div>
        {list.isLoading ? (
          <div className="muted" style={{ padding: 16 }}>加载中…</div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>类型</th>
                <th>等级</th>
                <th>来源</th>
                <th>运单</th>
                <th>状态</th>
                <th>责任方</th>
                <th>金额</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {items.map((e) => (
                <tr key={e.id}>
                  <td>{e.exception_type}</td>
                  <td><span className={`tag tag-${e.level === "high" ? "high" : e.level === "medium" ? "medium" : "low"}`}>{LEVEL_LABEL[e.level]}</span></td>
                  <td>{e.source}</td>
                  <td className="mono">{e.waybill_no || "-"}</td>
                  <td>{STATUS_LABEL[e.status] ?? e.status}</td>
                  <td>{e.responsibility_party || "-"}</td>
                  <td>{e.amount}</td>
                  <td className="row-actions">
                    {e.status !== "closed" && (
                      <>
                        <button className="btn-ghost" onClick={() => act.mutate({ id: e.id, action: "assign", body: { assignee: user?.id } })}>
                          指派给我
                        </button>
                        <button className="btn-ghost" onClick={() => act.mutate({ id: e.id, action: "handle", body: { resolution: "处理中" } })}>
                          处理
                        </button>
                        <button className="btn-ghost" onClick={() => act.mutate({ id: e.id, action: "close", body: { responsibility_party: "carrier", amount: 0, resolution: "已处理" } })}>
                          关闭
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
