import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 只读角色不该看到写按钮。
//
// 「财务（只读）」不是假想的角色，演示库里就有一个：
// finance.view + waybill.view + analytics.view，没有 finance.manage。
// 它打开对账中心，台账上五个「确认」、两个「核销」、「批量审计」
// 「+ 生成对账单」全都亮着，点下去弹一句「无财务操作权限」。
//
// 看得见按不动的按钮比没有按钮更糟：只读的人会以为是自己操作错了，
// 反复点、然后来问同事或提工单。而这一页原先一个 hasPerm 都没有。
//
// 浏览器侧有 role-walk.mjs 真的登进去数按钮；这条是它的静态搭档——
// 跑得快、不需要起服务，改坏了当场红。

const SRC = resolve(__dirname, "..");
const read = (p: string) => readFileSync(resolve(SRC, p), "utf8");

describe("写按钮要按权限显示", () => {
  it("对账中心的写动作都挂在 canManage 上", () => {
    const s = read("pages/ReconciliationPage.tsx");
    expect(s, "这一页没有引入权限判断").toMatch(/const canManage = hasPerm\(user, "finance\.manage"\)/);

    const lines = s.split("\n");
    const writeButtons = [
      "confirm.mutate",   // 确认对账单
      "setSettleTarget",  // 核销 / 登记核销 / 登记收付款
      "auditAll.mutate",  // 批量审计
      "auditOne.mutate",  // 审计本单
      "setShowGen",       // 生成对账单
    ];
    const naked: string[] = [];
    for (const marker of writeButtons) {
      const idxs = lines.flatMap((l, i) => (l.includes(marker) && l.includes("<button") ? [i] : []));
      expect(idxs.length, `找不到带 ${marker} 的按钮 —— 写法变了，这条用例正在空转`).toBeGreaterThan(0);
      for (const i of idxs) {
        // 往上看三行：条件通常写在按钮所在行，或它上面的那个 {cond && …}
        const window = lines.slice(Math.max(0, i - 3), i + 1).join("\n");
        if (!window.includes("canManage")) naked.push(`${marker}（第 ${i + 1} 行）`);
      }
    }
    expect(naked, `这些写按钮对只读角色也会显示：${naked.join("、")}。\n` +
      `  点下去只会弹一句"无权限"——只读的人会以为是自己操作错了。`).toEqual([]);
  });

  it("对手方下拉只在需要时才取，且要 finance.manage", () => {
    const s = read("pages/ReconciliationPage.tsx");
    const q = s.slice(s.indexOf('queryKey: ["cp", cpType]'));
    // 原先无条件预取 200 条对手方：一是白拉一次，二是财务角色没有
    // masterdata.view，一进页面就吃一个 403，而页面上什么都不说——
    // 只是那个下拉永远是空的。
    expect(q.slice(0, 600), "对手方下拉仍在无条件预取").toMatch(/enabled:\s*showGen && canManage/);
  });

  it("驾驶舱的证件块要按 masterdata.view 决定发不发请求", () => {
    const s = read("pages/ControlTowerPage.tsx");
    const c = s.slice(s.indexOf('queryKey: ["compliance-mini"]'));
    expect(c.slice(0, 400), "证件预警没有按 masterdata.view 控制 enabled")
      .toMatch(/enabled:\s*canMasterdata/);
    expect(s).toMatch(/const canMasterdata = hasPerm\(user, "masterdata\.view"\)/);
  });
});
