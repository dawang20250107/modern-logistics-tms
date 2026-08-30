import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// 令牌到期时并发的那几个请求，只能刷新一次。
//
// 刷新是**轮换**的：后端签发新券的同时把旧券作废（防重放的关键）。
// 而前端原先是谁 401 谁去刷，没有任何协调——驾驶舱一进来就并发发 5 个请求
// （运单统计、订单漏斗、工作台、财务敞口、证件预警），令牌到期时它们
// **同时** 401，于是 5 个刷新拿着同一张 refresh 打出去。
//
// 实测（对着真网关，同一张券并发换 5 次）：**3 个 200、2 个 401**。
// 而前端拿到 401 就 clearTokens()——刷新其实成功了，人却被踢下线。
// 用户看到的是"这系统隔一阵就把我踢出去"，而且只在页面开着好几个查询、
// 恰好赶上令牌到期时才出现，报上来也复现不了。

describe("令牌刷新只能有一次在飞", () => {
  const store = new Map<string, string>();
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.resetModules();
    store.clear();
    store.set("access", "old-access");
    store.set("refresh", "old-refresh");
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("五个请求同时 401，只发一次刷新", async () => {
    let refreshCalls = 0;
    let refreshed = false;
    fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      const u = String(url);
      if (u.includes("/auth/token/refresh")) {
        refreshCalls += 1;
        // 后端行为：旧券只能用一次，第二次拿它来换就是 401
        const body = JSON.parse(String(init?.body ?? "{}")) as { refresh?: string };
        if (body.refresh !== "old-refresh" || refreshed) {
          return new Response(JSON.stringify({ success: false, error: { code: "TOKEN_REVOKED", message: "凭证已失效" } }),
            { status: 401, headers: { "Content-Type": "application/json" } });
        }
        refreshed = true;
        return new Response(JSON.stringify({ success: true, data: { access: "new-access", refresh: "new-refresh" } }),
          { status: 200, headers: { "Content-Type": "application/json" } });
      }
      // 业务请求：旧令牌一律 401，新令牌放行
      const auth = new Headers(init?.headers).get("Authorization");
      if (auth === "Bearer new-access") {
        return new Response(JSON.stringify({ success: true, data: { ok: true } }),
          { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ success: false, error: { code: "TOKEN_INVALID", message: "过期" } }),
        { status: 401, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);

    const { apiGet } = await import("../api/client");
    // 驾驶舱那一屏：五个查询同时出发
    const results = await Promise.all([
      apiGet("/waybills/stats"), apiGet("/orders/funnel"), apiGet("/workbench"),
      apiGet("/finance/statement-overview"), apiGet("/credentials/expiring"),
    ]);

    expect(refreshCalls,
      `发了 ${refreshCalls} 次刷新。旧券只能用一次，多出来的那几次会拿到 401，` +
      `而前端拿到 401 就清空令牌——刷新其实成功了，人却被踢下线。`).toBe(1);
    expect(results).toHaveLength(5);
    expect(store.get("access"), "刷新成功后要换上新的 access").toBe("new-access");
    expect(store.get("refresh"), "轮换出来的新 refresh 也要存下").toBe("new-refresh");
  });

  it("刷新真的失败时，令牌要被清掉（不能把人留在半登录状态）", async () => {
    fetchMock = vi.fn(async (url: string) => {
      if (String(url).includes("/auth/token/refresh")) {
        return new Response(JSON.stringify({ success: false, error: { code: "TOKEN_REVOKED", message: "凭证已失效" } }),
          { status: 401, headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ success: false, error: { code: "TOKEN_INVALID", message: "过期" } }),
        { status: 401, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);

    const { apiGet } = await import("../api/client");
    await expect(apiGet("/waybills/stats")).rejects.toThrow();
    expect(store.get("access"), "刷新失败后必须清掉令牌").toBeUndefined();
  });
});
