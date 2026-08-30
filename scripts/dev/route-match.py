#!/usr/bin/env python3
"""前端调用的每个接口，后端是不是真有那条路由（且方法对得上）。

存在的理由：发布前这一轮抓到的问题里，有三个是同一种——前端把请求发到了
一个后端根本没注册的方法/路径上，于是恒定 405 或 404：

  · POST  /waybills/{no}/contract      「生成合同」，后端只注册了 GET
  · PATCH /waybills/{no}                批量「标记回单已回收」，同样只有 GET
  · POST  /receipts、/driver-credentials 发的是 multipart，引擎只解 JSON

这类错不报警：tsc 不看运行时路径，后端用例不知道前端发什么，
浏览器冒烟只加载页面不点按钮。而失败常被 catch 吞掉，界面照样报成功——
「已标记 0/5 条运单回单为「已回收」」配一个绿色对勾，就是这么来的。

用法：python3 scripts/dev/route-match.py   （退出码非 0 = 有对不上的）
"""
import re
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
MAIN = ROOT / "backend-go/cmd/server/main.go"
FRONT = ROOT / "frontend/src"

VERB = {"Get": "GET", "Post": "POST", "Patch": "PATCH", "Put": "PUT", "Delete": "DELETE",
        "Upload": "POST", "Download": "GET"}


def norm(p: str) -> str:
    """规约成便于比较的形式：去 query、去尾斜杠、参数段一律 *。"""
    p = re.sub(r"\$\{[^}]*\}", "*", p)   # 前端模板变量
    # 模板表达式里带引号时（`/orders/export${x ? "?" + y : ""}`），
    # 上面的取值正则会在那个引号处截断，留下一个没闭合的 ${。
    # 从那里切掉即可——后面本来就是拼 query 的，路径部分已经完整。
    if "${" in p:
        p = p[: p.index("${")]
    p = p.split("?")[0].rstrip("/")
    p = re.sub(r"\{[^}]*\}", "*", p)     # chi 路由参数
    return p


def backend_routes():
    """扫 main.go。

    路由挂在多个 router 变量上（r 公开、p 需鉴权、rt 子路由）。
    第一版只认 p.，于是把整批公开接口（登录、注册、公开查单）
    全报成"后端没有"——13 条里 12 条是误报。
    误报比漏报更伤：几次之后没人再看这个脚本的输出。
    """
    routes, mounts = set(), set()
    cur_mount = None
    for ln in MAIN.read_text().split("\n"):
        m = re.search(r'\b\w+\.Route\("(/api/v1[^"]*)"', ln)
        if m:
            cur_mount = m.group(1)
            mounts.add(norm(m.group(1)))
            continue
        m = re.search(r'\b(\w+)\.(Get|Post|Put|Patch|Delete)\("([^"]*)"', ln)
        if not m:
            continue
        var, verb, path = m.group(1), VERB[m.group(2)], m.group(3)
        if path.startswith("/api/v1"):
            routes.add((verb, norm(path)))
        elif cur_mount and var == "rt":
            rel = "" if path == "/" else path
            routes.add((verb, norm(cur_mount.rstrip("/") + rel)))
    return routes, mounts


def frontend_calls():
    calls = []
    for f in sorted(FRONT.rglob("*.ts*")):
        src = f.read_text()
        for m in re.finditer(
            r"api(Get|Post|Patch|Put|Delete|Upload|Download)\s*(?:<[^>]*>)?\s*\(\s*[`\"']([^`\"']*)", src
        ):
            path = m.group(2)
            if not path.startswith("/"):
                continue
            calls.append((VERB[m.group(1)], path, f"{f.relative_to(ROOT)}:{src[:m.start()].count(chr(10)) + 1}"))
    return calls


def main():
    routes, mounts = backend_routes()
    calls = frontend_calls()
    # 防空转：正则失效时两边都扫成空，比较结果恒为"全都对得上"。
    if not routes or not calls:
        print("路由表或调用点一个都没扫到——正则失效了，这条检查正在空转", file=sys.stderr)
        return 2

    missing = []
    for verb, path, where in calls:
        p = norm("/api/v1" + path)
        if (verb, p) in routes:
            continue
        # CRUD 挂载点：<mount> 与 <mount>/<id> 由通用引擎提供全套方法
        if any(p == m or p.startswith(m + "/") for m in mounts):
            continue
        # 末段是变量时（/orders/${id}/${action}），动作名运行时才定；
        # 该前缀下存在同方法的兄弟路由就认为接得上。
        if p.endswith("/*"):
            prefix = p[:-1]
            if any(v == verb and rp.startswith(prefix) for v, rp in routes):
                continue
        missing.append((verb, path, where))

    for v, p, w in missing:
        print(f"  ✗ {v:6} {p}\n      {w}")
    print(f"\n前端调用点 {len(calls)}，后端路由 {len(routes)}，对不上 {len(missing)}")
    return 1 if missing else 0


sys.exit(main())
