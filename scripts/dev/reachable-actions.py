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

**抓不到什么**（写在这里，免得有人以为它管得比实际宽）：

  · 只看动作路由。通用 CRUD 挂载点（p.Route(..., mdH.CRUD(...))）带出来的
    增删改查不在范围内——车联网的设备与地理围栏就是这样整套没有界面而
    这条检查看不见，只有它们的 ack/close 动作路由露了出来。
  · 匹配的是路径特征而不是真实调用。像 assign / close 这类常见词，
    另一个端点用了同一个词就会把它带过关：/orders/assign 存在时，
    /exceptions/{id}/assign 即使没人调也算"够得着"。
    收紧到逐字匹配又会被前端的模板串（`/carriers/${id}/blacklist`）挡住。
    宽一点、少误报，代价是会漏——漏掉的那些只能靠人走一遍界面。
  · 够得着 ≠ 好用。它只回答"有没有地方能点到"，不回答"点了对不对"。

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
}


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


def frontend_text():
    parts = []
    for p in (ROOT / "frontend/src").rglob("*.ts*"):
        parts.append(p.read_text())
    return "\n".join(parts)


def reachable(path, src):
    """前端源码里有没有打得到这个路径的调用点。

    路径参数在前端是模板串（`/carriers/${id}/blacklist`），没法逐字匹配，
    所以按「去掉参数后的各段」找：最后一段（动作名）必须出现，
    且它前面那个固定段也要出现——只匹配动作名的话，
    像 "read" "close" 这种常见词会到处都是。
    """
    segs = [s for s in path.split("/") if s and not s.startswith("{")]
    segs = [s for s in segs if s not in ("api", "v1")]
    if not segs:
        return True
    tail = segs[-1]
    if not re.search(r'[/"`\']' + re.escape(tail) + r'[`"\'/?]', src):
        return False
    if len(segs) >= 2:
        prev = segs[-2]
        if not re.search(r'[/"`\']' + re.escape(prev) + r'[`"\'/${]', src):
            return False
    return True


def main():
    rs = routes()
    src = frontend_text()
    # 防空转：任何一边扫成空，比较结果恒为"全都够得着"
    if not rs or len(src) < 1000:
        print("路由或前端源码没扫到——正则失效了，这条检查正在空转", file=sys.stderr)
        return 2

    unreachable, allowed = [], 0
    for verb, path in sorted(set(rs)):
        if path in ALLOW:
            allowed += 1
            continue
        if not reachable(path, src):
            unreachable.append(f"{verb} {path}")

    for u in unreachable:
        print(f"  ✗ {u}")
        print("      后端有这个动作，前端源码里找不到调用点——功能在界面上够不着。")
        print("      要么把入口加上，要么在 reachable-actions.py 的 ALLOW 里写明为什么不需要。")
    print(f"\n动作路由 {len(set(rs))} 条，已声明无需界面入口 {allowed} 条，够不着 {len(unreachable)} 条")
    return 1 if unreachable else 0


sys.exit(main())
