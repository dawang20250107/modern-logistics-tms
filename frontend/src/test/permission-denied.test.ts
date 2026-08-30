import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

import { ApiError, isPermissionDenied } from "../api/client";

// 403 不是"出错了"。
//
// 发布前把 22 个读写配置补齐权限点之后，403 一下子变多了，
// 一个原本就在的问题跟着显形：页面拿到 403 之后显示的是
// 「加载失败：数据获取出错，请重试或稍后再来」，还配一颗重试按钮——
// 而真相是这个角色就是看不了这一块，点一万次也一样。
//
// 实测（演示客服打开资源库的承运商页）：
//   修复前：加载失败 数据获取出错，请重试或稍后再来。[重试]
//   修复后：无访问权限 你的角色没有被授予相应权限点。请让管理员…勾上。
//
// StateView 里本来就有 forbidden 这一态（图标是把锁），只是没人把 403 接到它上面。
//
// 这条用例守两件事：
//   1. isPermissionDenied 认得后端两种拼写（通用 CRUD 引擎回小写、
//      各 handler 回大写）——少认一种就等于这条链断了。
//   2. 拿到查询错误的地方要把它传给 StateView，而不是只传一句写死的文案。

describe("403 要说成没权限，不是出错了", () => {
  it("两种拼写都认得，别的错不误伤", () => {
    expect(isPermissionDenied(new ApiError("permission_denied", "缺少所需权限。"))).toBe(true);
    expect(isPermissionDenied(new ApiError("PERMISSION_DENIED", "无运单查看权限"))).toBe(true);
    expect(isPermissionDenied(new ApiError("403", "x"))).toBe(true);
    // 这些都不是权限问题，不能被改写成"没权限"——那会把真故障藏起来
    expect(isPermissionDenied(new ApiError("INTERNAL", "服务器内部错误"))).toBe(false);
    expect(isPermissionDenied(new ApiError("not_found", "未找到。"))).toBe(false);
    expect(isPermissionDenied(new ApiError("throttled", "请求已被限流"))).toBe(false);
    expect(isPermissionDenied(new Error("boom"))).toBe(false);
    expect(isPermissionDenied(null)).toBe(false);
  });

  it("能拿到查询错误的 StateView 都把它传下去了", () => {
    const SRC = resolve(process.cwd(), "src");
    const files: string[] = [];
    const walk = (dir: string) => {
      for (const e of readdirSync(dir, { withFileTypes: true })) {
        const p = join(dir, e.name);
        if (e.isDirectory()) walk(p);
        else if (e.name.endsWith(".tsx")) files.push(p);
      }
    };
    walk(SRC);

    // 判据：一个 kind="error" 的 StateView，如果它的 onRetry 里能看到
    // `xxx.refetch()`，那 `xxx.error` 就是现成的，没有理由不传。
    const missing: string[] = [];
    let seen = 0;
    for (const f of files) {
      const src = readFileSync(f, "utf8");
      // 属性里有 onRetry={() => …}，那个箭头里的 ">" 会把 [^>]* 截断——
      // 第一版就是这么写的，结果一处都没扫到，被防空转那一行拦下来了。
      for (const m of src.matchAll(/<StateView\b([\s\S]*?)\/>/g)) {
        const attrs = m[1];
        if (!/kind="error"/.test(attrs)) continue;
        const retry = /onRetry=\{\(\) => (\w+)\.refetch\(\)\}/.exec(attrs);
        if (!retry) continue;
        seen++;
        if (!new RegExp(`error=\\{${retry[1]}\\.error\\}`).test(attrs)) {
          missing.push(`${f.replace(SRC, "")} → ${retry[1]}`);
        }
      }
    }
    // 防空转：一处都没扫到说明写法变了，而不是"都合规"
    expect(seen, "一个可判的 StateView 都没扫到——正则失效了，这条用例正在空转").toBeGreaterThan(10);
    expect(
      missing,
      "这些地方手上有 error 却没传给 StateView，403 会被显示成「加载失败，请重试」：\n  " +
        missing.join("\n  "),
    ).toEqual([]);
  });
});
