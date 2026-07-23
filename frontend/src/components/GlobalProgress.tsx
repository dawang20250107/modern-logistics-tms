import { useIsFetching, useIsMutating } from "@tanstack/react-query";
import { useEffect, useState } from "react";

// 全局请求进度条：任意后台查询/写操作进行中时，顶部滑动一道青光，提升「有反馈」感知。
// 140ms 去抖：瞬时/缓存命中不闪条，只有真的在等网络才提示。
export function GlobalProgress() {
  const busy = useIsFetching() + useIsMutating() > 0;
  const [show, setShow] = useState(false);
  useEffect(() => {
    if (!busy) { setShow(false); return; }
    const t = setTimeout(() => setShow(true), 140);
    return () => clearTimeout(t);
  }, [busy]);
  return <div className={`gp-bar${show ? " on" : ""}`} aria-hidden />;
}
