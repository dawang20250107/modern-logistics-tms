import type { ReactNode } from "react";

import { toast } from "../api/toast";

// 单号一键复制：订单号/运单号/对账单号等展示型编码，点击即复制（日常高频操作）。
export function CopyCode({ value, className = "", children }: { value: string; className?: string; children?: ReactNode }) {
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(`已复制 ${value}`);
    } catch {
      toast.error("复制失败，请手动选择");
    }
  };
  return (
    <span
      className={`copycode ${className}`}
      title="点击复制"
      role="button"
      tabIndex={0}
      onClick={(e) => { e.stopPropagation(); void copy(); }}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); void copy(); } }}
    >
      <span className="copycode-txt">{children ?? value}</span>
      <svg className="copycode-ic" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
        <rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
      </svg>
    </span>
  );
}
