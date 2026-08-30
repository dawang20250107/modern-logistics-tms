import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 回单核验：界面上必须能核验，也必须能驳回。
//
// 后端一直有 POST /receipts/{id}/confirm，前端一次都没调过——
// 运单详情的回单区只有「上传」「查看原件」和一个 OCR 徽标，
// 连每张回单自己是"待核验/已核验/已驳回"哪一档都不显示。
//
// 少了这一块的后果不是"某个功能不好用"，是**结算凭据是假的**：
// 界面上唯一能动回单状态的地方是运单列表那个批量「标已回收」，
// 它直接把运单写成 returned，不看下面挂的是哪张回单。
// 实测库里就是这个样子——运单 LT-000002516 显示"回单已回收"，
// 而它下面五张回单全都还是 uploaded，一张都没人核过。
//
// 驳回那半边更要紧。后端是按「这张运单还有没有通过核验的回单」
// 重算的：核验通过 → 运单 audited，改判驳回 → 退回 returned。
// 没有驳回按钮，一张错核的回单就再也退不回来，
// 运单会一直挂着"凭证齐全"等着放款。实测两个方向都通：
// confirmed → audited，rejected → returned。
//
// 这条查的是形状，不是行为——行为由后端 receiptconfirm_test.go 钉着。
// 形状会悄悄没：改版时把按钮删了，编译照过、类型照过、页面照样渲染。

const PAGE = resolve(__dirname, "../pages/WaybillDetailPage.tsx");

describe("回单核验的界面入口", () => {
  const src = readFileSync(PAGE, "utf8");

  it("调的是 /receipts/{id}/confirm，不是运单上那个批量标记", () => {
    expect(src).toMatch(/apiPost\(`\/receipts\/\$\{[^}]+\}\/confirm`/);
  });

  // 断言必须落在**按钮**上，不能落在 mutation 上。
  // 第一版写的是 toContain('status: "confirmed"')，回测时把整块按钮删掉
  // 它照样绿——因为 mutation 的类型签名里就有
  // `status: "confirmed" | "rejected"` 这段字符。
  // 那样查的是"有没有人写过这个 mutation"，而不是"界面上按不按得到"，
  // 恰恰漏掉这条检查要防的那件事：按钮被改版删掉、mutation 留在原地。
  it("核验通过与驳回两个方向都真的挂在按钮上", () => {
    expect(src).toMatch(/confirmReceipt\.mutate\(\{[^}]*status: "confirmed"[^}]*\}\)/);
    expect(src).toMatch(/confirmReceipt\.mutate\(\{[^}]*status: "rejected"[^}]*\}\)/);
  });

  it("每张回单自己的核验状态要渲染出来（不是只 import 了词表）", () => {
    expect(src).toMatch(/POD_STATUS_LABEL\[[a-z]+\.status\]/);
  });

  it("状态词表与后端 wbstatus.ValidPOD 的全集一致", async () => {
    const { POD_STATUS_LABEL } = await import("../api/types");
    expect(Object.keys(POD_STATUS_LABEL).sort()).toEqual(
      ["confirmed", "rejected", "uploaded"],
    );
  });
});
