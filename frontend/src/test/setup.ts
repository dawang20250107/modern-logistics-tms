import "@testing-library/jest-dom/vitest";
import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
  // DataTable 把列显隐/宽度/排序/密度等视图状态存 localStorage，
  // 不清的话上一个用例的视图会漏进下一个，测试之间互相污染。
  localStorage.clear();
});

// jsdom 没有实现这些，但组件在挂载/滚动/自适宽时会用到
Element.prototype.scrollIntoView = vi.fn();
if (!window.matchMedia) {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: vi.fn(), removeListener: vi.fn(),
    addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
}
