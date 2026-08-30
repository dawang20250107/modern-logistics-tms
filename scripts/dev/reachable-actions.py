#!/usr/bin/env python3
"""后端有这个动作，前端有没有地方能点到它。

route-match.py 查的是反方向：前端调了一个后端没有的路径（恒定 404/405）。
这一条查的是**功能写好了、界面上够不着**——这一轮它出现了六次：

  · 承运合同的三个 mutation 写好了没渲染
  · 司机报销写好了没渲染
  · 计价规则的阶梯价：表单默认就是按重量阶梯，而没有一处能填那几档价
  · 异常闭环的后半截（指派/处理/定责关闭）四个端点全的，界面只能上报
  · 承运商拉黑：表格里有「黑名单」标签、筛选器里有这一档，就是没法设上
  · 登录解锁：连续失败会锁账号，而管理员在系统里解不开

这一类特别难发现：不报错、不崩、类型检查也过，页面看起来正常——
只是少了一块，而少的那块没人知道它本该在。**逐页点按钮也发现不了，
因为那颗按钮压根不存在。**

判据：main.go 里注册的每一个动作路由（POST/PATCH/DELETE，且不是通用 CRUD
挂载点自动带出来的那批），前端源码里必须出现得了它的路径特征。
够不到的必须在 ALLOW 里写明为什么——写不出理由的，多半就是漏了。

查两件事：
  1. 动作路由（POST/PATCH/DELETE）前端有没有调用点
  2. 通用 CRUD 挂载点前端有没有用过

第 2 条是补第 1 条的盲区：车联网的设备与地理围栏整套没有界面，
而它们只有 ack/close 两个动作路由露了出来，光查动作路由看不见整块的缺失。

**抓不到什么**（写在这里，免得有人以为它管得比实际宽）：

  · 够得着 ≠ 好用。它只回答"有没有地方能点到"，不回答"点了对不对"。
    真按一遍、再去库里看结果，仍然得人来做。
  · 调用点得是**整串写出来的**路径。如果哪天有人把它拼成
    `${base}/${resource}/confirm`（连资源名都是变量），这里看不穿，
    会误报成"够不着"。误报会被人查，比漏报安全。
  · 动作名那一格是变量时（`/orders/${id}/${action}`），按"这个词在
    同一个文件里以字符串出现过"算数。同文件里恰好有个同名的枚举值
    或字段名，就会放过去——比原先按整个前端找宽松一点点，但也就一点点。

**这条检查自己犯过的错**（值得记在这里）：

  原先的口径是把整个前端拼成一个大字符串，再看"路径最后一段"和
  "它前面那一段"分别出现过没有。这两次出现可以来自**毫不相干的两个文件**，
  于是它判的其实是"这两个词在前端出现过吗"。

  代价是双向的。误报那一侧：车联网告警的 ack 被判成"够得着"，
  因为 `/ack` 来自司机端提醒确认、`"alerts"` 来自承运商列表的一个表格列。
  漏报那一侧更贵——/receipts/{id}/confirm（回单核验，结算认的就是它）
  和 /ai/deepseek/chat 真的没人调，却因为别处有 confirm、别处有 deepseek
  一路放行。**一条会放行的检查比没有检查更糟**：它让人以为这里查过了。

退出码：0 全都够得着 / 1 有够不着的 / 2 空转（正则失效）
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# 允许「前端够不着」的端点，每条都要写清是给谁用的。
# 这个名单是**故意**要写起来别扭的：加一条就得说明白它为什么不需要界面入口。
ALLOW = {
    "/api/v1/auth/token/verify": "给外部集成校验令牌用，管理端自己不需要",
    "/api/v1/telematics/ingest": "车载终端/GPS 供应商上报，不是人点的",
    "/api/v1/tracking/points": "同上，轨迹点上报",
    "/api/v1/finance/payment-results": "外部 OA 回写付款结果，见交付说明",
    "/api/v1/ai/query-waybill": "AI 问答的内部调用，入口是搜索框不是这个路径",
    "/api/v1/analytics/metrics/query": "指标查询的通用入口，页面走的是各自的专用端点",
    "/api/v1/orders/{id}/convert": "订单转运单由派单流程内部调用，不单独给按钮",
    "/api/v1/orders/{id}/unassign": "取消指派：派单抽屉重新指派时内部走这条，没有独立入口",
    "/api/v1/waybills/{no}/events": "运单事件由系统各处写入，不给人工入口",
    "/api/v1/waybills/{no}/partial-sign": "部分签收：司机端与回单流程内部使用",
    "/api/v1/drivers/{id}/refresh-stats": "司机累计数据重算，运维动作",
    "/api/v1/driver-credentials/{id}/ocr": "OCR 尚未接入任何引擎，见 docs/delivery-notes.md",
    "/api/v1/reminders/{id}/acknowledge": "提醒确认走司机端 /driver/reminders/{id}/ack",
    # 车联网告警：设备、地理围栏、告警这一整套后端都有，**管理端没有任何界面**。
    # 发布前不补，因为系统本身不采集定位（依赖终端 MQTT/HTTP 上报，见交付说明
    # 「这套系统当前不做的事」），没有数据源的情况下做一套配置界面是空转。
    # 已写进 docs/delivery-notes.md，交付时明说，而不是让客户自己发现。
    "/api/v1/telematics/alerts/{id}/ack": "车联网告警：整个模块没有管理界面，已在交付说明里写明",
    "/api/v1/telematics/alerts/{id}/close": "同上",
    # 从 Django 平移过来的裸 LLM 透传（见 backend-go/PORTING.md）。
    # 产品里的 AI 入口是 /agent/chat（带工具、带业务上下文），
    # 这条只是把 messages 原样转给上游，没有、也不该有界面按钮。
    "/api/v1/ai/deepseek/chat": "裸 LLM 透传，平移自 Django；产品 AI 入口走 /agent/chat",
    # 运单级的这四条与订单级的同名动作是平移期留下的两套并行接口。
    # 界面统一走订单级那套，理由写在 INSTEAD 里由检查自己去核。
    "/api/v1/waybills/merge": "界面合单在订单上",
    "/api/v1/waybills/{no}/split": "界面拆单在订单上",
    "/api/v1/waybills/dispatch-plan": "界面批量排线在订单上",
    "/api/v1/waybills/{no}/dispatch": "界面派车在订单上",
}


# 有些豁免的理由是「界面走的是另一条」。**这种理由本身会过期**：
# 那条替代路径哪天没人调了，这条豁免就从"有等价入口"变成"两条都够不着"，
# 而名单还在一口咬定没事——正是这一轮要治的那个毛病。
# 所以替代路径写成可校验的，让检查自己去核，而不是写成一句话让人去信。
INSTEAD = {
    "/api/v1/waybills/merge": "/api/v1/orders/merge",
    "/api/v1/waybills/{no}/split": "/api/v1/orders/{id}/split",
    "/api/v1/waybills/dispatch-plan": "/api/v1/orders/dispatch-plan",
    "/api/v1/waybills/{no}/dispatch": "/api/v1/orders/{id}/dispatch",
    "/api/v1/reminders/{id}/acknowledge": "/driver/reminders/{id}/ack",
}


# 后端有 CRUD 接口、管理端没有界面的资源。同样每条都要写清为什么。
# 这些不是"忘了"——是发布前有意识地不做，理由写在这里也写在交付说明里，
# 客户问起来能答上，而不是让他自己撞上。
CRUD_ALLOW = {
    "/api/v1/finance/contracts": "框架合同：合同价这条更精确的匹配路径没有界面，"
                                 "报价走的是按客户/线路通配那条。要用合同价需单独排期",
    "/api/v1/finance/expense-items": "费用科目词表在代码里（internal/expitem），这张表未启用",
    "/api/v1/finance/expense-records": "费用明细通过运单详情的「录费用」增改，不单开管理页",
    "/api/v1/finance/payment-requests": "付款申请由报销审批自动生成，付款动作在报销面板上；"
                                        "跨运单的付款队列视图未做",
    "/api/v1/finance/webhooks": "对外推送配置没有界面，需要时用接口配",
    "/api/v1/finance/webhook-deliveries": "同上，投递记录只能走接口查",
    "/api/v1/org/departments": "部门层级没有独立管理页，员工的组织归属在员工名录里选",
    "/api/v1/org/employee-groups": "员工组没有界面，授权走的是角色",
    "/api/v1/org/permissions": "权限点目录由代码维护（auth.EnsurePermissions），界面只读矩阵",
    "/api/v1/reminders": "司机提醒在运单详情页发送与查看，这个 CRUD 挂载点是冗余的",
    "/api/v1/routes": "线路主数据没有管理页，线路名在订单/规则里直接填",
    "/api/v1/telematics/alerts": "车联网整块没有管理界面，见交付说明",
    "/api/v1/telematics/devices": "同上",
    "/api/v1/telematics/geofences": "同上",
}


def crud_mounts():
    src = (ROOT / "backend-go/cmd/server/main.go").read_text()
    return sorted({m.group(1) for m in re.finditer(r'\.Route\(\s*"(/api/v1/[^"]+)"', src)})


def routes():
    """从 main.go 里取动作路由，带上 Route(...) 前缀。"""
    src = (ROOT / "backend-go/cmd/server/main.go").read_text()
    out, stack = [], []  # stack: [(前缀, 进入时的花括号深度)]
    depth = 0
    for line in src.splitlines():
        mount = re.search(r'\.Route\(\s*"([^"]+)"', line)
        opens = line.count("{") - line.count("}")
        if mount:
            stack.append((mount.group(1), depth))
        for m in re.finditer(r'\.(Post|Patch|Delete|Put)\(\s*"([^"]+)"', line):
            verb, path = m.group(1).upper(), m.group(2)
            full = path if path.startswith("/api/") else (stack[-1][0] + path if stack else path)
            out.append((verb, re.sub(r"/+$", "", full) or "/"))
        depth += opens
        while stack and depth <= stack[-1][1]:
            stack.pop()
    return out


# 前端源码里像 URL 的字符串字面量。`${API_BASE}` 这类前缀去掉，
# 路径参数（`${id}`）统一写成 {}，查询串和锚点截掉。
URL_LIT = re.compile(r"""["'`]((?:\$\{[^}]*\})?/[^"'`\s]*)["'`]""")
# 动作名候选：同文件里出现过的小写短横线单词（"collect-cod"、"approve"…）
WORD_LIT = re.compile(r"""["'`]([a-z][a-z0-9-]{1,40})["'`]""")


def call_paths():
    """把前端所有调用点规整成 (路径, 同文件里出现过的词) 的列表。

    原先这里是把整个前端拼成一个大字符串、再按「路径的最后一段和它前面
    那段分别出现过」来判断。**这两次出现可以来自毫不相干的两个文件**，
    于是判断的其实是「这两个词在前端出现过吗」，而不是「有没有人调它」。

    车联网告警的 ack 就是这么被判成「够得着」的：
    `/ack` 来自司机端的提醒确认，`"alerts"` 来自承运商列表里一个叫
    alerts 的表格列——两个文件、两件事，凑出一个不存在的调用点。

    反方向更值钱：/receipts/{id}/confirm 和 /ai/deepseek/chat 这两条
    真的没人调，旧口径却因为别处有 confirm、别处有 deepseek 而放行了。
    **一条会放行的检查比没有检查更糟**，因为它让人以为这里已经查过了。

    同时带上「同文件里的词」是为了看穿这种通用派发：

        mutationFn: (action: string) => apiPost(`/orders/${id}/${action}`, {})

    这个字面量规整完是 /orders/{}/{}，段数对得上就能配上 /orders 下
    任何一个三段路由——包括根本没人传的那些。所以动作名那一格
    不能无条件通配：只有当这个词**在同一个文件里以字符串出现过**
    （即确实有调用点把它传进去），才算数。
    """
    out = []
    for p in sorted((ROOT / "frontend/src").rglob("*.ts*")):
        text = p.read_text()
        words = {m.group(1) for m in WORD_LIT.finditer(text)}
        for m in URL_LIT.finditer(text):
            s = re.sub(r"^\$\{[^}]*\}", "", m.group(1))
            s = re.sub(r"\$\{[^}]*\}", "{}", s)
            s = s.split("?")[0].split("#")[0]
            s = re.sub(r"/+$", "", s)
            if s.startswith("/"):
                out.append((s, words))
    return out


def same_path(route, lit, words):
    """逐段比。段数必须一样。

    路由的 {id} 是通配——前端那一格填什么都对得上。
    前端的 {} 只在两种情况下算数：对上路由的 {id}（就是个路径参数），
    或者这个动作名在同一个文件里出现过（确实有人把它传进去了）。
    """
    a = [x for x in route.split("/") if x]
    b = [x for x in lit.split("/") if x]
    if len(a) != len(b):
        return False
    for x, y in zip(a, b):
        if x.startswith("{"):
            continue
        if y == "{}":
            if x not in words:
                return False
            continue
        if x != y:
            return False
    return True


def rel_of(path):
    return path[len("/api/v1"):] if path.startswith("/api/v1") else path


def reachable(path, calls):
    """前端有没有一个调用点整串就是这个路径。"""
    rel = rel_of(path)
    return any(same_path(rel, c, w) or same_path(path, c, w) for c, w in calls)


def used_resource(mount, calls):
    """CRUD 挂载点算「用过」：有调用点正好是它，或落在它下面。"""
    rel = rel_of(mount)
    return any(c == rel or c.startswith(rel + "/") for c, _ in calls)


def main():
    rs = routes()
    calls = call_paths()
    # 防空转：任何一边扫成空，比较结果恒为"全都够得着"（或恒为"全都够不着"）
    if not rs or len(calls) < 50:
        print(f"路由 {len(rs)} 条、前端调用点 {len(calls)} 处——正则失效了，"
              "这条检查正在空转", file=sys.stderr)
        return 2

    unreachable, allowed = [], 0
    orphan = []  # 豁免理由说「界面走另一条」，而那一条自己也够不着
    stale = []   # 名单里登记了"界面上够不着"，而实际上前端已经在调了
    gone = []    # 名单里登记的路由/资源已经不存在了
    for verb, path in sorted(set(rs)):
        if path in ALLOW:
            allowed += 1
            # 名单自检：**前端已经调上了的条目，豁免就是过期的**。
            # 过期条目比没有名单更坏：它让人以为"这块是有意不做界面的"，
            # 于是再没人去看。车载上报那两条就是被一句与事实不符的豁免理由
            # 藏了很久（写着"走设备凭据"，而它们既没有设备凭据、
            # 也不在设备侧的路由组上）。
            if reachable(path, calls):
                stale.append(f"{verb} {path}")
            elif path in INSTEAD and not reachable(INSTEAD[path], calls):
                orphan.append(f"{verb} {path} → {INSTEAD[path]}")
            continue
        if not reachable(path, calls):
            unreachable.append(f"{verb} {path}")
    live_paths = {p for _, p in rs}
    gone += [p for p in ALLOW if p not in live_paths]

    mounts = crud_mounts()
    if not mounts:
        print("一个 CRUD 挂载点都没扫到——正则失效了，这条检查正在空转", file=sys.stderr)
        return 2
    gone += [m for m in CRUD_ALLOW if m not in mounts]
    dead_res, res_allowed = [], 0
    for mount in mounts:
        if mount in CRUD_ALLOW:
            res_allowed += 1
            if used_resource(mount, calls):
                stale.append(f"{mount}（整个资源）")
            continue
        if not used_resource(mount, calls):
            dead_res.append(mount)

    for x in sorted(stale):
        print(f"  ✗ {x}")
        print("      名单里说它界面上够不着，而前端已经在调了——这条豁免过期了，删掉。")
        print("      名单一旦有一条不可信，整份名单就都不可信了。")
    for o in sorted(orphan):
        print(f"  ✗ {o}")
        print("      这条豁免的理由是「界面走的是另一条」，而那一条前端也没人调了——")
        print("      于是两条都够不着，而名单还在说这里有等价入口。")
    for g in sorted(gone):
        print(f"  ✗ {g}")
        print("      名单里登记的这条路由/资源已经不存在了，删掉。")
    for u in unreachable:
        print(f"  ✗ {u}")
        print("      后端有这个动作，前端源码里找不到调用点——功能在界面上够不着。")
        print("      要么把入口加上，要么在 reachable-actions.py 的 ALLOW 里写明为什么不需要。")
    for d in dead_res:
        print(f"  ✗ {d}（整个资源）")
        print("      后端有一整套增删改查，前端一次都没调过——这块功能没有界面。")
        print("      要么做界面，要么在 CRUD_ALLOW 里写明为什么不做，并同步写进交付说明。")
    print(f"\n动作路由 {len(set(rs))} 条（声明无需入口 {allowed}，够不着 {len(unreachable)}）；"
          f"CRUD 资源 {len(mounts)} 个（声明无界面 {res_allowed}，未声明的够不着 {len(dead_res)}）")
    return 1 if (unreachable or dead_res or stale or gone or orphan) else 0


sys.exit(main())
