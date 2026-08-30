import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 登录之后的每一页，要么挂权限门，要么写明为什么不用挂。
//
// 侧栏按权限点过滤过了，但那只挡住"点得到"。详情页是靠链接进的：
// 客户在邮件里发一个订单链接、同事贴一个运单地址、浏览器收藏夹——
// 都会绕过侧栏直接落到页面上。
//
// 而后端补齐权限闸之后，这些页面会变成一屏 403：
// 接口全红，页面上要么空白要么散落着"加载失败"，就是不说
// "你的角色没有这个权限"。RequirePerm 存在就是为了说这句话，
// 只是三条路由当初漏了挂：orders/:id、waybills/:no、intake。
//
// intake 那条尤其难受：客服工作台原先谁都点得进去，表单能填满，
// 在提交那一下才告诉你缺权限——填了一屏才被拒，比进门就被拒糟得多。

const APP = readFileSync(resolve(__dirname, "../App.tsx"), "utf8");

/** 允许不挂权限门的路由，每条都要写清为什么。 */
const OPEN: Record<string, string> = {
  "/": "运输驾驶舱：内容按权限分块（工作台的池中待派要 waybill.view），没有权限的人看到的是自己的那几项",
  profile: "个人资料，看的是自己",
  audit: "审计日志按 is_staff 判，不是权限点；页面自己有无权限态",
  admin: "老书签重定向到 /org，不渲染内容",
  "*": "404 兜底",
};

describe("登录之后的页面都有权限门", () => {
  it("每条路由要么有 RequirePerm，要么在名单里", () => {
    // 只看 ProtectedRoute 里面的那一段：外面的登录、找回口令、公开查单、
    // 客户自助下单、司机端本来就免登录，用名字排除法迟早漏（/register 就漏过）。
    const start = APP.indexOf("<ProtectedRoute />}>");
    expect(start, "找不到 ProtectedRoute —— 路由写法变了，这条用例正在空转").toBeGreaterThan(0);
    const guarded = APP.slice(start, APP.indexOf("</Routes>", start));
    const inLayout = [...guarded.matchAll(/<Route\s+(?:path="([^"]*)"|index)([\s\S]*?)\/>/g)];

    // 空转防护：正则漂了（改了写法、换了组件名）会一条都匹配不到，
    // 那时候"全都合规"和"根本没检查"长得一模一样。
    expect(inLayout.length).toBeGreaterThan(8);

    const naked: string[] = [];
    for (const m of inLayout) {
      const path = m[1] ?? "/";
      const body = m[2] ?? "";
      if (path in OPEN) continue;
      if (/RequirePerm/.test(body)) continue;
      naked.push(path);
    }
    expect(naked, `这些登录后的路由没有权限门：${naked.join(", ")}。` +
      `直接敲地址或点链接进来的人会落到一屏 403 上，而页面不会告诉他缺的是什么权限。`).toEqual([]);
  });

  it("客服工作台要建单权限，且调度员不能被误挡", () => {
    // 客服有 waybill.create，调度员只勾了 waybill.manage（manage 涵盖建单）。
    // 只判一个点会把调度员挡在门外，而后端是放行的——
    // "界面说没权限、接口其实可以"比直接报错更难查。
    const intake = APP.match(/<Route path="intake"[\s\S]*?\/>/)?.[0] ?? "";
    expect(intake).toMatch(/waybill\.create/);
    expect(intake).toMatch(/waybill\.manage/);
  });

  it("名单里的每一条都还是真实存在的路由", () => {
    for (const path of Object.keys(OPEN)) {
      if (path === "/") {
        expect(APP).toMatch(/<Route index/);
        continue;
      }
      expect(APP, `名单里登记的 ${path} 已经不是一条路由了，该清理`).toContain(`path="${path}"`);
    }
  });
});
