import { useQuery } from "@tanstack/react-query";

import { apiGet } from "../api/client";
import { toast } from "../api/toast";
import type { ReplyCardData } from "../api/types";

// 客服查单回复卡：客户问一句，10 秒可复制回复
export function ReplyCard({ waybillNo }: { waybillNo: string }) {
  const q = useQuery({
    queryKey: ["reply-card", waybillNo],
    queryFn: () => apiGet<ReplyCardData>(`/waybills/${waybillNo}/reply-card`),
    enabled: Boolean(waybillNo),
  });

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success("已复制客户回复文案");
    } catch {
      toast.error("复制失败，请手动选择文本");
    }
  };

  if (q.isLoading) return <div className="muted small">生成回复卡…</div>;
  const c = q.data;
  if (!c) return <div className="muted small">未取到回复卡。</div>;

  return (
    <div className="reply-card">
      <pre>{c.copy_text}</pre>
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <button className="btn-primary" style={{ padding: "6px 12px" }} onClick={() => copy(c.copy_text)}>复制回复</button>
        {c.driver_phone && <a className="btn-ghost" style={{ padding: "6px 12px" }} href={`tel:${c.driver_phone}`}>呼叫司机</a>}
        {c.exception && <span className="tag tag-medium">异常：{c.exception}</span>}
      </div>
    </div>
  );
}
