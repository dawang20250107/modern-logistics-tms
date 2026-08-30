import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

// 能力探测失败时，不能当成"能力可用"。
//
// 命令栏那处原先写的是：
//
//   const aiReady = aiStatus.data?.configured !== false;
//
// 请求**失败**时 data 是 undefined，`undefined !== false` 为真——判成"可用"。
// 没有 ai.use 权限点的账号（探测接口回 403）就落进这一档：
// 界面照样亮着「Enter ↵」，按下去才拿到 403。
//
// 而这段代码上面自己的注释写的正是相反的事：
// 「那是个不会因为"稍后重试"而改变的状态，所以要在用户按下去之前就告诉他，
// 而不是等他试了再说一句含糊的失败。」
//
// 这类判断有个共同的形状：**用 `data?.x !== 值` 直接当布尔用**。
// 只要请求可能失败，它就会把失败读成成功。所以这条用例查形状，
// 而不是查某一处的行为——同样的写法在别处出现时一样要红。

const SRC = resolve(__dirname, "..");

function read(p: string) {
  return readFileSync(resolve(SRC, p), "utf8");
}

describe("能力探测失败要算作不可用", () => {
  it("AI 状态：403 或任何失败都不能判成可用", () => {
    const s = read("components/SpotlightCommandBar.tsx");
    const line = s.split("\n").find((l) => l.includes("const aiReady"));
    expect(line, "找不到 aiReady —— 变量改名了，这条用例正在空转").toBeTruthy();
    // 必须先看有没有出错，再看返回值
    expect(line, `aiReady 只看了 data，没看 isError：${line}\n` +
      `  请求失败时 data 是 undefined，"undefined !== false" 会把失败判成可用。`)
      .toMatch(/isError/);
  });

  it("AI 用不了时要分清是没接入还是没权限", () => {
    const s = read("components/SpotlightCommandBar.tsx");
    expect(s, "没有区分 403 —— 「未接入」会让人去找运维配 API Key，" +
      "而真实原因是这个角色没有 ai.use 权限点").toMatch(/isPermissionDenied/);
    expect(s).toMatch(/没有「使用 AI 助手」权限点/);
  });

  it("驾驶舱的财务块要按权限决定发不发请求", () => {
    const s = read("pages/ControlTowerPage.tsx");
    // 驾驶舱对所有角色开放，里面的财务聚合却要 finance.view。
    // 照发不误的话，客服一打开首页就吃 403，界面显示"财务数据暂不可用"
    // 加一颗重试按钮——读起来像系统故障，实则永远不会好。
    const fin = s.slice(s.indexOf('queryKey: ["statement-overview"]'));
    expect(fin.slice(0, 400), "statement-overview 没有按 finance.view 控制 enabled")
      .toMatch(/enabled:\s*canFinance/);
    expect(s, "没有 canFinance —— 财务磁贴和面板要跟着一起收").toMatch(/const canFinance = hasPerm\(user, "finance\.view"\)/);
  });
});
