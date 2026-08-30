// 起浏览器的唯一出口。五个走查脚本共用。
//
// 为什么要抽出来：原先每个脚本里都写死了两条本机路径——
//   import("/opt/node22/lib/node_modules/playwright/index.js")
//   chromium.launch({ executablePath: "/opt/pw-browsers/chromium" })
// 这两条只在这台开发机上成立。CI 上 playwright 装在 node_modules 里、
// 浏览器由 playwright 自己管，两条路径都不存在，脚本一启动就崩。
//
// 这正是「浏览器类检查进不了 CI」的技术原因之一，而这一晚上查出来的
// 严重问题（司机端跨域整个不能用、打卡按钮永久卡死、阶梯价界面上填不了）
// **全都是浏览器走查抓到的，CI 一条都跑不了**。把路径变成可配置的，
// 这批检查才有可能进 CI。
//
// 解析顺序：
//   PLAYWRIGHT_PKG   —— playwright 包的入口（默认先试本机全局装的那份，
//                       不在就按普通模块名解析，走 node_modules）
//   CHROMIUM_PATH    —— 浏览器可执行文件；不设就让 playwright 用它自己下的那份
import { statSync } from "node:fs";

export async function launchBrowser(opts = {}) {
  const { chromium } = await loadPlaywright();
  const exe = process.env.CHROMIUM_PATH ?? defaultChromium();
  try {
    return await chromium.launch({
      // --no-sandbox：容器里跑必须的（CI runner 与本机沙箱都是 root）
      args: ["--no-sandbox"],
      ...(exe ? { executablePath: exe } : {}),
      ...opts,
    });
  } catch (e) {
    // 浏览器起不来时要说清是"没装"还是"起不来"。
    // 直接把 playwright 的原始报错抛出去的话，看到的是一大段栈，
    // 而真正该做的事（跑一句 playwright install）藏在里面。
    console.error("✗ 浏览器起不来，这条走查没跑起来：");
    console.error("  " + String(e).split("\n")[0]);
    console.error(`  可执行文件：${exe || "(交给 playwright 自己找)"}`);
    console.error("  CI 上跑一次 npx playwright install --with-deps chromium；" +
      "本机可用 CHROMIUM_PATH 指定。");
    process.exit(2);
  }
}

async function loadPlaywright() {
  const explicit = process.env.PLAYWRIGHT_PKG;
  const candidates = explicit
    ? [explicit]
    : ["/opt/node22/lib/node_modules/playwright/index.js", "playwright"];
  const errs = [];
  for (const c of candidates) {
    try {
      const m = await import(c);
      return m.default ?? m;
    } catch (e) {
      errs.push(`${c}: ${e.message?.split("\n")[0]}`);
    }
  }
  console.error("✗ 找不到 playwright，这条走查没跑起来：\n  " + errs.join("\n  "));
  console.error("  本机：npm i -g playwright；CI：npm ci 之后设 PLAYWRIGHT_PKG=playwright");
  process.exit(2); // 2 = 没跑起来，见 lib/browser-login.mjs 的退出码约定
}

function defaultChromium() {
  // 本机沙箱里浏览器在这个固定位置；不存在就返回空，交给 playwright 自己找。
  // 先 stat 再 launch，而不是 try/catch 一个 launch：launch 的报错很难看出
  // 到底是"路径不对"还是"浏览器起不来"。
  try {
    return statSync("/opt/pw-browsers/chromium").isFile() ? "/opt/pw-browsers/chromium" : "";
  } catch {
    return "";
  }
}
