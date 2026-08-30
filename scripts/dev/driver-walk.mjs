// 司机端走查：真在浏览器里按一遍 /driver 的按钮，然后回库核对。
//
// 为什么单独有这一条：司机端是**客户的司机每天真正在用的那个界面**，
// 而此前所有浏览器走查（smoke-ui / e2e-flow / write-paths / ui-audit）
// 一个都没打开过它——它们全都从管理端 /login 进去。
// 这一轮已经吃过一次亏：演示司机的身份证号是空串，司机端一个都登不进去，
// 是从零起库那条链才发现的，而不是任何一条界面走查。
//
// 打卡是**第四条上传路径**。前三条（订单附件、回单、司机证件）查出四个问题，
// 全部返回 2xx。所以这里的验收标准和那三条一样：
// **传上去之后取回来，看到的是不是你刚传的那张。**
//
// 用法（需先起好 :8000 网关与 :5173 前端）：
//   node scripts/dev/driver-walk.mjs [baseUrl]
// 退出码：0 过 / 1 有发现 / 2 没跑起来（对齐其余走查脚本）
import { writeFileSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { launchBrowser } from "./lib/browser.mjs";

const BASE = process.argv[2] ?? "http://127.0.0.1:5173";
const API = process.env.API_BASE ?? "http://127.0.0.1:8000";
const DB = process.env.DATABASE_URL ?? "postgres://tms:tms@127.0.0.1:5432/tms";
const EXIT_NOT_RUN = 2;

const q = (sql) => execFileSync("psql", [DB, "-tAq", "-c", sql], { encoding: "utf8" }).trim();
const fail = [];
const note = (s) => console.log("  " + s);
const bad = (s) => { console.log("  ✗ " + s); fail.push(s); };

// ── 取一条真实可打卡的运单 ──
const row = q(`SELECT d.phone || '|' || right(d.id_no,6) || '|' || w.waybill_no || '|' || w.status
  FROM ops_waybill w JOIN md_driver d ON d.id = w.driver_id
  WHERE w.status IN ('dispatched','loaded','departed','in_transit')
    AND coalesce(d.id_no,'') <> '' AND length(d.id_no) >= 6
  ORDER BY w.created_at DESC LIMIT 1`);
if (!row) {
  console.error("✗ 库里没有「已派给一个有身份证号的司机、且还没走完」的运单，没法走这条链。");
  console.error("  这不是「走查没过」，是「走查没跑」。先跑 seed 或造一单再来。");
  process.exit(EXIT_NOT_RUN);
}
const [phone, tail, wbNo, status0] = row.split("|");
note(`用例：司机 ${phone}（证件尾号 ${tail}）· 运单 ${wbNo} · 当前 ${status0}`);

// 每次跑用不同的图，否则"取回来的那张"可能是上一次留下的，检查会一直绿
const MARK = Date.now().toString(36);
const PHOTO = "/tmp/driver-walk-" + MARK + ".jpg";

const before = Number(q(`SELECT count(*) FROM ops_driver_checkin c JOIN ops_waybill w ON w.id=c.waybill_id
  WHERE w.waybill_no='${wbNo}'`));

// ── 预检：司机端带的自定义头必须被 CORS 放行 ──
// 单独先验一次，是为了**报错时能说对原因**。少了这个头的表现是：
// 登录（不带自定义头）200，之后 /driver/tasks 被浏览器在预检阶段挡下，
// 服务端日志一行都没有，界面只写 "Failed to fetch"。
// 不先验的话，下面会报成"司机登录没成功"，而登录其实是成功的。
{
  const pre = await fetch(`${API}/api/v1/driver/tasks`, {
    method: "OPTIONS",
    headers: { Origin: BASE, "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "x-driver-token" },
  });
  const allow = (pre.headers.get("access-control-allow-headers") ?? "").toLowerCase();
  if (allow.includes("x-driver-token")) note("✓ 预检放行 X-Driver-Token");
  else bad(`预检没放行 X-Driver-Token（Access-Control-Allow-Headers: ${allow || "(空)"}）——` +
           "跨域部署时司机端登录后一片空白，服务端什么都看不到");
}

const browser = await launchBrowser();
// 司机端是手机上用的：视口按手机来，桌面视口下有些布局问题根本不出现。
//
// **故意不给定位权限**。这不是省事，是这条走查最要紧的一个条件：
// 未授权（以及弹窗没答复）时 getCurrentPosition 的两个回调一个都不会来，
// 连它自己的 timeout 选项都不管用——实测 15 秒。打卡流程如果直接 await 它，
// 按钮就永久卡在"正在定位…"，这个司机再也打不了卡。
// 现实里司机不点那个权限弹窗是常态，所以走查必须在"没授权"这一侧跑。
// 谁要是哪天给这个 context 加上 geolocation 权限，这条回归就废了。
const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
const page = await ctx.newPage();
const pageErrors = [];
page.on("pageerror", (e) => pageErrors.push(String(e).split("\n")[0].slice(0, 160)));
const writes = [];
page.on("response", (r) => {
  if (!["POST", "PATCH", "PUT", "DELETE"].includes(r.request().method())) return;
  writes.push({ path: r.url().replace(/^https?:\/\/[^/]+/, "").split("?")[0], status: r.status() });
});

try {
  // ── 1. 登录（手机号 + 证件后 6 位）──
  await page.goto(`${BASE}/driver`, { waitUntil: "networkidle" });
  const inputs = await page.$$("input");
  if (inputs.length < 2) {
    console.error("✗ 司机端登录页上没找到两个输入框——页面没打开或改版了。");
    process.exit(EXIT_NOT_RUN);
  }
  await inputs[0].fill(phone);
  await inputs[1].fill(tail);
  await page.locator(".drv-login-submit").click();
  // 登进去的标志：出现任务卡片或"暂无任务"
  await page.waitForSelector(".drv-card, .drv-empty", { timeout: 15000 }).catch(() => {});
  if (await page.locator(".drv-login-submit").count()) {
    const msg = await page.locator(".drv-feedback, .auth-error, [role=alert]").first()
      .textContent({ timeout: 1000 }).catch(() => "");
    console.error(`✗ 司机登录没成功，还停在登录页${msg ? "：" + msg.trim() : ""}`);
    console.error("  这不是「走查没过」，是「走查没跑」。检查网关是否在，以及演示司机有没有身份证号。");
    process.exit(EXIT_NOT_RUN);
  }
  note("✓ 司机登录（手机号 + 证件后 6 位）");

  // ── 2. 本人运单在任务列表里 ──
  const shown = await page.locator(".drv-card").allTextContents();
  if (shown.join(" ").includes(wbNo)) note(`✓ 任务列表里能看到 ${wbNo}`);
  else bad(`任务列表里没有 ${wbNo}（司机看不到自己的单，打卡链就断在这）`);

  // ── 3. 拍照打卡：真选一张图按下去 ──
  // 图用浏览器 canvas 现画一张再编码成 JPEG。
  // 第一版是手写一段 base64 塞进去，结果那串是**截断的**——Go 侧
  // image.Decode 报 "invalid JPEG format"，Watermark 走"非图片原样返回"的分支，
  // 于是脚本报「水印没打上」。那是我的样本坏了，不是产品坏了。
  // 凭证类检查的样本必须是真能解码的图，否则查出来的是自己造的假问题。
  const jpegB64 = await page.evaluate((mark) => {
    const c = document.createElement("canvas");
    c.width = 640; c.height = 480;
    const g = c.getContext("2d");
    g.fillStyle = "#4a6fa5"; g.fillRect(0, 0, 640, 480);
    g.fillStyle = "#fff"; g.font = "28px sans-serif";
    g.fillText("DRIVER-WALK " + mark, 40, 240);
    for (let i = 0; i < 200; i++) { // 加点噪声，免得压缩得太狠、水印前后差别看不出来
      g.fillStyle = `hsl(${(i * 7) % 360},70%,60%)`;
      g.fillRect((i * 37) % 640, (i * 53) % 480, 9, 9);
    }
    return c.toDataURL("image/jpeg", 0.92).split(",")[1];
  }, MARK);
  writeFileSync(PHOTO, Buffer.from(jpegB64, "base64"));
  note(`样本图 ${readFileSync(PHOTO).length} 字节（浏览器 canvas 生成，真 JPEG）`);

  const card = page.locator(".drv-card").filter({ hasText: wbNo }).first();
  const btn = card.locator(".drv-main-btn");
  if (!(await btn.count())) {
    bad(`${wbNo} 上没有打卡按钮（状态 ${status0} 应该有下一步）`);
  } else {
    const label = (await btn.textContent())?.trim();
    await card.locator('input[type="file"]').setInputFiles(PHOTO);
    // 打卡前会先定位，getCurrentPosition 的超时是 5 秒（弱网友好，定位失败也放行）。
    // 等 4 秒是不够的——第一版就这么等，结果 POST 根本还没发出去，
    // 脚本报「库里没有打卡记录」，看起来像后端丢数据。等到请求真的落地为止。
    await page.waitForResponse(
      (r) => r.url().includes("/driver/checkin") && r.request().method() === "POST",
      { timeout: 20000 },
    ).catch(() => bad("按下打卡后 20 秒内没有发出 /driver/checkin 请求"));
    await page.waitForTimeout(1500);
    note(`按下「${label}」`);
  }
  // ── 4. 证件自助上传 ──
  // 这是**第五条上传路径**，而且和管理端「资源库传证件」不是同一个接口：
  // 那条走通用 CRUD 引擎，这条是司机端自己的 handler，各解各的 multipart。
  // 前面四条上传路径查出四个问题、全部返回 2xx，所以这条一样要真传一次再取回来。
  const credBefore = Number(q(`SELECT count(*) FROM md_driver_credential
    WHERE driver_id=(SELECT id FROM md_driver WHERE phone='${phone}') AND self_uploaded`));
  const credUp = page.locator(".driver-wb").filter({ hasText: "证件上传" });
  if (!(await credUp.count())) {
    bad("司机端没有「证件上传」这一块——司机没法自助建档");
  } else {
    await credUp.locator('input[type="file"]').setInputFiles(PHOTO);
    await page.waitForResponse(
      (r) => r.url().includes("/driver/credentials") && r.request().method() === "POST",
      { timeout: 20000 },
    ).catch(() => bad("按下「选择照片」后 20 秒内没有发出 /driver/credentials 请求"));
    await page.waitForTimeout(1500);
    const credRow = q(`SELECT coalesce(file,'')||'|'||ocr_status FROM md_driver_credential
      WHERE driver_id=(SELECT id FROM md_driver WHERE phone='${phone}') AND self_uploaded
      ORDER BY created_at DESC LIMIT 1`);
    const credAfter = Number(q(`SELECT count(*) FROM md_driver_credential
      WHERE driver_id=(SELECT id FROM md_driver WHERE phone='${phone}') AND self_uploaded`));
    if (credAfter !== credBefore + 1) {
      bad(`证件记录没有 +1（${credBefore} → ${credAfter}）——界面报"已上传"而库里没有`);
    } else {
      const [credFile, ocr] = credRow.split("|");
      if (!credFile) {
        bad("证件记录里 file 是空的——图传上去了但没落库（saveMedia 失败被吞掉了？）");
      } else {
        const r = await fetch(`${API}/media/${credFile}`);
        const b = Buffer.from(await r.arrayBuffer());
        if (r.status !== 200) bad(`证件图取不回来：GET /media/${credFile} → ${r.status}`);
        else if (!b.equals(readFileSync(PHOTO))) bad(`取回来的证件图和刚传的那张不一样（${b.length} vs ${readFileSync(PHOTO).length} 字节）`);
        else note(`✓ 证件自助上传：/media/${credFile} 取回来就是刚传的那张（ocr_status=${ocr}）`);
      }
    }
  }

} finally {
  await browser.close();
}

// ── 4. 回库核对：打卡记录、状态推进、照片 ──
const after = Number(q(`SELECT count(*) FROM ops_driver_checkin c JOIN ops_waybill w ON w.id=c.waybill_id
  WHERE w.waybill_no='${wbNo}'`));
if (after === before + 1) note(`✓ 打卡记录 +1（${before} → ${after}）`);
else bad(`打卡记录没有 +1（${before} → ${after}）——界面报成功而库里没有`);

const rec = q(`SELECT coalesce(c.photo,'') || '|' || c.node || '|' || coalesce(c.lat::text,'') || '|' || w.status
  FROM ops_driver_checkin c JOIN ops_waybill w ON w.id=c.waybill_id
  WHERE w.waybill_no='${wbNo}' ORDER BY c.created_at DESC LIMIT 1`);
const [photo, node, , status1] = rec.split("|");
note(`最新打卡：节点 ${node} · 运单状态 ${status0} → ${status1}`);
if (status1 === status0) bad(`打卡没推进运单状态（仍是 ${status0}）——司机按了但调度那边看不到进展`);

// 这一条是整个脚本的重点：照片存没存、取不取得回来。
// 前三条上传路径都是"返回 2xx 但字节丢了"，只有真去取一次才看得出来。
if (!photo) {
  bad("打卡记录里 photo 是空的——照片传上去了但没落库（saveMedia 失败被吞掉了？）");
} else {
  const resp = await fetch(`${API}/media/${photo}`);
  const buf = Buffer.from(await resp.arrayBuffer());
  if (resp.status !== 200) bad(`照片取不回来：GET /media/${photo} → ${resp.status}`);
  else if (buf.length < 1024) bad(`照片取回来只有 ${buf.length} 字节，不像一张图`);
  else if (buf.subarray(0, 2).toString("hex") !== "ffd8") bad(`取回来的不是 JPEG（前两字节 ${buf.subarray(0, 2).toString("hex")}）`);
  else note(`✓ 照片取得回来：/media/${photo}（${buf.length} 字节，JPEG）`);
  // 水印是这张照片作为凭证的全部价值所在。字节数和原图一模一样，
  // 就说明 Watermark 走了"非图片/没字体，原样返回"那三条静默分支之一——
  // 打卡照片看起来存下来了，其实是一张没有时间、没有 GPS、没有运单号的裸照片。
  if (buf.length === readFileSync(PHOTO).length) {
    bad("取回来的字节数和原图完全一样——水印没打上（没字体？图没解码？），凭证等于一张裸照片");
  }
}

note(`写请求：${writes.map((w) => w.status + " " + w.path).join("、") || "（一个都没有）"}`);
const badWrites = writes.filter((w) => w.status >= 400);
if (badWrites.length) for (const w of badWrites) bad(`写请求 ${w.status} ${w.path}`);
if (pageErrors.length) for (const e of pageErrors) bad(`未捕获异常：${e}`);

console.log(fail.length
  ? `\n✗ 司机端发现 ${fail.length} 个问题`
  : "\n✓ 司机端：登录 → 看到本人运单 → 拍照打卡 → 证件自助上传 → 回库核对（记录、状态推进、两张图都取得回来）");
process.exit(fail.length ? 1 : 0);
