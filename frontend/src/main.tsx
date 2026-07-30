import "@fontsource-variable/inter";
import { MutationCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { ApiError } from "./api/client";
import { toast } from "./api/toast";
import { App } from "./App";
import { ConfirmDialog } from "./components/ConfirmDialog";
import { Toaster } from "./components/Toaster";
import "./styles.css";
import { initTheme } from "./api/theme";

// 主题初始化（亮为主 + 暗可切换）：在渲染前落 data-theme，避免明暗闪烁
initTheme();

// 全局兜底：任何未被组件接管的写操作失败都给用户明确反馈。
// 已配置 onError 的 mutation 由组件展示更贴合场景的文案，避免同一错误连弹两次。
const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } },
  mutationCache: new MutationCache({
    onError: (error, _vars, _ctx, mutation) => {
      if (mutation.meta?.silent || mutation.options.onError) return;
      const msg = error instanceof ApiError ? error.message : "操作失败，请稍后重试";
      toast.error(msg);
    },
  }),
});

const container = document.getElementById("root");
if (!container) throw new Error("#root not found");

createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
      <Toaster />
      <ConfirmDialog />
    </QueryClientProvider>
  </StrictMode>,
);
