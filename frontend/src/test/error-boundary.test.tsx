import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ErrorBoundary } from "../components/ErrorBoundary";

// 渲染期抛错时，整个应用不能变成一张白页。
//
// 实测过原状（在计价规则页里人为抛一个错）：
//   页面正文 **0 个字符**，侧栏没了，顶栏没了，没有任何提示。
// React 在没有 error boundary 时会把整棵树卸载掉。用户看到一片空白，
// 不会知道换个地址还能用（实测确实能），只会认为"系统挂了"。
//
// 触发它不需要多离奇的情况：一条脏数据、一个没想到的 null、
// 一个日期解析失败——而这套系统要处理的是导进来的、外部系统回写的、
// 司机手机上传的数据。
//
// 修好之后：侧栏顶栏还在，内容区显示「「计价规则」这一页出错了」+ 错误原文。

function Boom(): React.ReactElement {
  throw new Error("脏数据：cargo_weight_ton 是 undefined");
}

describe("渲染出错不能白屏", () => {
  it("接住异常，说清是哪一页，并给出错误原文", () => {
    // React 会把这个错原样打到 console.error，测试输出里会很吵；静音它，
    // 但**只静音这一处**——别的用例里的 console.error 还要能看见。
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <ErrorBoundary where="计价规则">
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText(/「计价规则」这一页出错了/)).toBeTruthy();
    // 原文要显示出来：用户截图反馈时这一行能直接定位
    expect(screen.getByText(/cargo_weight_ton 是 undefined/)).toBeTruthy();
    // 还得有出路，不能只告诉人"坏了"
    expect(screen.getByText("重试这一页")).toBeTruthy();
    spy.mockRestore();
  });

  it("没出错时原样渲染子树（别把正常页面也吞了）", () => {
    render(
      <ErrorBoundary where="订单管理">
        <div>正常内容</div>
      </ErrorBoundary>,
    );
    expect(screen.getByText("正常内容")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

// 光有组件还不够：它得真的被接在路由上。
// 这一层是静态检查——AppLayout 的 Outlet 必须被 ErrorBoundary 包着，
// App 最外层也要有一层。摘掉任何一层，白屏就回来了。
describe("兜底真的接在路由上", () => {
  it("AppLayout 的 Outlet 被 ErrorBoundary 包着，App 最外层也有一层", async () => {
    const { readFileSync } = await import("node:fs");
    const { resolve } = await import("node:path");
    const layout = readFileSync(resolve(process.cwd(), "src/components/AppLayout.tsx"), "utf8");
    const app = readFileSync(resolve(process.cwd(), "src/App.tsx"), "utf8");
    expect(
      /<ErrorBoundary[^>]*>\s*<Outlet \/>/.test(layout),
      "AppLayout 里的 <Outlet /> 没有被 ErrorBoundary 包住——" +
        "页面一抛错，侧栏和顶栏会跟着一起消失",
    ).toBe(true);
    expect(
      app.includes("<ErrorBoundary>"),
      "App 最外层没有 ErrorBoundary——布局本身崩了就没人接了",
    ).toBe(true);
  });
});
