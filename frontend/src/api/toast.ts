// 轻量全局 toast：模块级发布订阅，供 React Query 全局错误回调与组件共用。
import { useSyncExternalStore } from "react";

export type ToastKind = "error" | "success" | "info";
export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

let toasts: Toast[] = [];
let seq = 0;
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

export function dismissToast(id: number) {
  toasts = toasts.filter((t) => t.id !== id);
  emit();
}

export function pushToast(message: string, kind: ToastKind = "info", ttlMs = 5000) {
  const id = ++seq;
  toasts = [...toasts, { id, kind, message }];
  emit();
  if (ttlMs > 0) {
    setTimeout(() => dismissToast(id), ttlMs);
  }
  return id;
}

export const toast = {
  error: (m: string) => pushToast(m, "error", 7000),
  success: (m: string) => pushToast(m, "success", 4000),
  info: (m: string) => pushToast(m, "info", 5000),
};

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

export function useToasts(): Toast[] {
  return useSyncExternalStore(subscribe, () => toasts, () => toasts);
}
