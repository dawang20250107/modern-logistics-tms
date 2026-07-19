import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";

import { apiGet, apiPost } from "../api/client";
import { fmtRelative } from "../api/format";
import type { Notification, Paginated } from "../api/types";
import { useEventStream } from "../api/useEventStream";

export function NotificationBell() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // 点击面板外部自动关闭
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const count = useQuery({
    queryKey: ["ntf", "count"],
    queryFn: () => apiGet<{ unread: number }>("/notifications/unread-count"),
    refetchInterval: 30000,
  });
  const list = useQuery({
    queryKey: ["ntf", "list"],
    queryFn: () => apiGet<Paginated<Notification>>("/notifications?page_size=15"),
    enabled: open,
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["ntf"] });

  useEventStream((e) => {
    if (e.type === "notification") invalidate();
  });

  const readAll = useMutation({ mutationFn: () => apiPost("/notifications/read-all", {}), onSuccess: invalidate });
  const readOne = useMutation({
    mutationFn: (id: string) => apiPost(`/notifications/${id}/read`, {}),
    onSuccess: invalidate,
  });

  const unread = count.data?.unread ?? 0;
  const items = list.data?.items ?? [];

  return (
    <div style={{ position: "relative" }} ref={ref}>
      <button
        className="btn-ghost"
        onClick={() => setOpen((v) => !v)}
        aria-label="通知"
        style={{ position: "relative", display: "inline-flex", alignItems: "center", justifyContent: "center", padding: "7px 9px", color: "var(--ink-2)" }}
      >
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
          <path d="M13.73 21a2 2 0 0 1-3.46 0" />
        </svg>
        {unread > 0 && (
          <span style={{
            position: "absolute", top: -6, right: -6, background: "var(--red)", color: "#fff",
            borderRadius: 999, fontSize: 10, fontWeight: 700, padding: "1px 6px", minWidth: 16, textAlign: "center",
          }}>{unread > 99 ? "99+" : unread}</span>
        )}
      </button>
      {open && (
        <div className="panel" style={{ position: "absolute", right: 0, top: 42, width: 340, zIndex: 50, boxShadow: "var(--shadow-lg)" }}>
          <div className="panel-head">
            通知
            <button className="btn-ghost" onClick={() => readAll.mutate()}>全部已读</button>
          </div>
          <div style={{ maxHeight: 380, overflow: "auto" }}>
            {items.length === 0 ? (
              <div className="muted small" style={{ padding: 16 }}>暂无通知</div>
            ) : (
              items.map((n) => (
                <button
                  key={n.id}
                  type="button"
                  onClick={() => {
                    if (!n.is_read) readOne.mutate(n.id);
                    if (n.link_type === "order" && n.link_id) { setOpen(false); navigate(`/orders/${n.link_id}`); }
                  }}
                  style={{
                    display: "block", width: "100%", textAlign: "left", border: "none", font: "inherit", color: "inherit",
                    padding: "10px 16px", borderBottom: "1px solid var(--line)", cursor: "pointer",
                    background: n.is_read ? "transparent" : "var(--accent-weak)",
                  }}
                >
                  <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                    <span className={`tag tag-${n.level === "critical" ? "high" : n.level === "warning" ? "medium" : "low"}`}>
                      {n.level === "critical" ? "重要" : n.level === "warning" ? "提醒" : "信息"}
                    </span>
                    <b style={{ fontSize: 13 }}>{n.title}</b>
                    <span className="small muted" style={{ marginLeft: "auto" }}>{fmtRelative(n.created_at)}</span>
                  </div>
                  {n.body && <div className="small muted" style={{ marginTop: 4 }}>{n.body}</div>}
                </button>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}
