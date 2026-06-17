import { useEffect, useRef, useState } from "react";

import { API_BASE_URL, getAccess } from "./client";

export interface LiveEvent {
  type: string;
  data: Record<string, unknown>;
  t: number;
}

export function useEventStream(onEvent?: (e: LiveEvent) => void): LiveEvent[] {
  const [events, setEvents] = useState<LiveEvent[]>([]);
  const cb = useRef(onEvent);
  cb.current = onEvent;

  useEffect(() => {
    const token = getAccess();
    if (!token) return;
    const es = new EventSource(`${API_BASE_URL}/stream/events?token=${encodeURIComponent(token)}`);
    es.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as { type: string; data: Record<string, unknown> };
        const item: LiveEvent = { type: msg.type, data: msg.data, t: Date.now() };
        setEvents((prev) => [item, ...prev].slice(0, 20));
        cb.current?.(item);
      } catch {
        /* 忽略非 JSON 心跳 */
      }
    };
    return () => es.close();
  }, []);

  return events;
}
