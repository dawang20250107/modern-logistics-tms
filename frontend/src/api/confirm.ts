// 全局确认对话框：promise 化，供不可逆操作前调用 `await confirmAction({...})`。
import { useSyncExternalStore } from "react";

export interface ConfirmRequest {
  id: number;
  title: string;
  message: string;
  confirmText: string;
  tone: "danger" | "normal";
  resolve: (ok: boolean) => void;
}

let current: ConfirmRequest | null = null;
let seq = 0;
const listeners = new Set<() => void>();
const emit = () => {
  for (const l of listeners) l();
};

export function confirmAction(opts: {
  title?: string;
  message: string;
  confirmText?: string;
  tone?: "danger" | "normal";
}): Promise<boolean> {
  return new Promise((resolve) => {
    current = {
      id: ++seq,
      title: opts.title ?? "请确认",
      message: opts.message,
      confirmText: opts.confirmText ?? "确认",
      tone: opts.tone ?? "normal",
      resolve,
    };
    emit();
  });
}

function settle(ok: boolean) {
  if (!current) return;
  current.resolve(ok);
  current = null;
  emit();
}

export function useConfirm() {
  const req = useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => current,
    () => current,
  );
  return { request: req, resolve: settle };
}
