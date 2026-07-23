import { useEffect, useRef, useState } from "react";

import { API_BASE_URL, getAccess } from "./client";

export interface LiveEvent {
  type: string;
  data: Record<string, unknown>;
  t: number;
}

type StreamListener = (event: LiveEvent) => void;

const listeners = new Set<StreamListener>();
let sharedStream: EventSource | null = null;
let sharedToken = "";

function ensureStream(): void {
  const token = getAccess();
  if (!token) return;
  if (sharedStream && sharedToken === token) return;

  sharedStream?.close();
  sharedToken = token;
  sharedStream = new EventSource(`${API_BASE_URL}/stream/events?token=${encodeURIComponent(token)}`);
  sharedStream.onmessage = (event) => {
    try {
      const message = JSON.parse(event.data) as { type: string; data: Record<string, unknown> };
      const item: LiveEvent = { type: message.type, data: message.data, t: Date.now() };
      listeners.forEach((listener) => listener(item));
    } catch {
      // Heartbeats and non-JSON server messages are intentionally ignored.
    }
  };
}

// All consumers share one EventSource. `collect` is opt-in because current
// callers only react to events and should not re-render to maintain a history.
export function useEventStream(onEvent?: (event: LiveEvent) => void, collect = false): LiveEvent[] {
  const [events, setEvents] = useState<LiveEvent[]>([]);
  const callbackRef = useRef(onEvent);
  callbackRef.current = onEvent;

  useEffect(() => {
    const listener: StreamListener = (item) => {
      if (collect) setEvents((previous) => [item, ...previous].slice(0, 20));
      callbackRef.current?.(item);
    };

    listeners.add(listener);
    ensureStream();
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) {
        sharedStream?.close();
        sharedStream = null;
        sharedToken = "";
      }
    };
  }, [collect]);

  return events;
}
