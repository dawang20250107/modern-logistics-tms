// 从 vitest/config 引 defineConfig（而非 vite）：test 字段的类型在这里
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8000"
    }
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    // 只跑单元/组件测试
    include: ["src/**/*.test.{ts,tsx}"],
    css: false,
  },
});
