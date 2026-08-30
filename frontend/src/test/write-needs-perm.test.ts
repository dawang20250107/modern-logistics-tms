import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 会发写请求的界面，必须先判权限。
//
// 后端每一条写路由都挂了闸（那是安全边界，不能靠前端）。前端这一层
// 管的是另一件事：**别让人看见一颗按不动的按钮**。
//
// 实测：拿只有 waybill.view 的演示客服打开一张它范围内的运单，
// 看到的按钮和超管**一模一样**——「已送达」「发送提醒」「生成费用」
// 「保存」「提交报销」「生成合同」全在，点下去一律弹"无权限"。
// 财务（只读）在对账中心看到五个「确认」两个「核销」，在计价规则
// 看到「新增/编辑/删除」。客服在订单管理看到「确认」「进池」，
// 在调度台看到「锁定」「派单」「智能排线拼单」。
//
// 对操作的人来说，一屏可点却点不动的按钮就是"系统坏了"——
// 他会重试、会问同事、会提工单，而没有一处告诉他缺的是哪个权限点。
//
// 所以这条查形状：文件里出现了写请求（apiPost/apiPatch/apiDelete/apiUpload），
// 就必须出现 hasPerm。例外逐条写理由——写不出理由的多半就是漏了。

const SRC = resolve(__dirname, "..");

/** 允许没有权限判断的文件，每条必须说清为什么。 */
const EXEMPT: Record<string, string> = {
  "pages/CustomerOrderPage.tsx": "对外开放的客户自助下单，免登录，本来就没有权限点可判",
  "components/NotificationBell.tsx": "把自己的通知标记为已读，没有别人的数据可动",
  "components/ExceptionRegisterModal.tsx":
    "上报异常刻意只要 waybill.view —— 发现问题的常常是只有查看权的客服，登记的门要低",
  "components/StructuredOrderForm.tsx":
    "只在客服工作台里用，那一页整页挂着 RequirePerm（waybill.create 或 waybill.manage）",
  "components/BatchDispatchModal.tsx":
    "只从调度台「批量派承运商」进，那颗按钮已按 waybill.manage 收起",
  "pages/WaybillsPage.tsx":
    "嵌在订单管理的「运单」页签里，写动作只有导出用的列视图保存，不涉及业务数据",
};

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const e of readdirSync(join(SRC, dir), { withFileTypes: true })) {
    if (e.isDirectory()) out.push(...walk(join(dir, e.name)));
    else if (e.name.endsWith(".tsx")) out.push(join(dir, e.name));
  }
  return out;
}

describe("会写数据的界面要先判权限", () => {
  it("每个有写请求的文件要么判权限，要么在名单里", () => {
    const files = [...walk("pages"), ...walk("components")];
    // 空转防护：目录结构或后缀变了会一个文件都扫不到
    expect(files.length, "一个 .tsx 都没扫到 —— 目录结构变了，这条用例正在空转").toBeGreaterThan(20);

    const withWrites: string[] = [];
    const naked: string[] = [];
    for (const rel of files) {
      const s = readFileSync(join(SRC, rel), "utf8");
      const writes = s.match(/mutationFn:[^\n]*\b(apiPost|apiPatch|apiDelete|apiUpload|apiPut)\b/g);
      if (!writes) continue;
      withWrites.push(rel);
      if (s.includes("hasPerm(")) continue;
      if (rel.split(/[\\/]/).join("/") in EXEMPT) continue;
      naked.push(`${rel}（${writes.length} 处写请求）`);
    }

    // 同样是空转防护：一个带写请求的文件都没找到，说明正则失配了
    expect(withWrites.length, "没找到任何带写请求的文件 —— 正则多半失配了").toBeGreaterThan(8);

    expect(naked, `这些界面会写数据但没有任何权限判断：\n  ${naked.join("\n  ")}\n\n` +
      `权限不够的人会看到一颗按不动的按钮，点下去弹一句"无权限"。\n` +
      `要么按权限收起来，要么写进 EXEMPT 并说明为什么不需要。`).toEqual([]);
  });

  it("名单里的每个文件都还在，且确实有写请求", () => {
    for (const rel of Object.keys(EXEMPT)) {
      let s = "";
      try {
        s = readFileSync(join(SRC, rel), "utf8");
      } catch {
        expect.fail(`名单里登记的 ${rel} 已经不存在了，该清理`);
      }
      expect(s, `${rel} 已经没有写请求了，从名单里去掉`)
        .toMatch(/mutationFn:[^\n]*\b(apiPost|apiPatch|apiDelete|apiUpload|apiPut)\b/);
    }
  });

  // 还差一半：**这条豁免还需不需要**。文件后来自己判了权限时，
  // 名单里那句"为什么不用判"就成了假话。门是有的，名单却说这里不设门——
  // 不造成漏洞，但读名单的人会以为这一屏是有意不判的，
  // 于是下次真漏判时也没人多看一眼。名单一旦有一条不可信，整份就都不可信。
  it("名单里的每个文件都还确实不需要判权限", () => {
    const stale = Object.keys(EXEMPT).filter((rel) => {
      try {
        return /\bhasPerm\b/.test(readFileSync(join(SRC, rel), "utf8"));
      } catch {
        return false;
      }
    });
    expect(stale, "这些文件已经自己判权限了，名单里那条理由是假话，删掉").toEqual([]);
  });
});
