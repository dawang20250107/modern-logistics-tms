#!/usr/bin/env python3
"""状态守卫必须和它守着的那次写在同一个事务里。

后端有一类写法：

    var status string
    h.DB.QueryRow(ctx, "SELECT status FROM … WHERE id=$1").Scan(&status)
    if status != "submitted" { 409 }
    h.DB.Exec(ctx, "UPDATE … SET status='…' WHERE id=$1")   // 不带状态条件

串行点两次会被第一条挡住，看起来是好的。两个人同时点、或者前端重试／
双击，两边都读到旧状态、都通过检查、都往下写——守卫等于不存在。

实测代价（修之前）：
  · 6 个并发关闭同一条异常 → 6 个都返回 200，落 6 条应付，
    800 元的赔付计成 4800
  · 审批和驳回同时发生 → 驳回抢输了照样把已审批的报销覆盖成"已驳回"，
    而应付和付款申请已经落库，账上挂着一笔已驳回却仍要付的钱

所以这里查的是：handler 里既有"读状态做前置校验"又有写库，
但整段没开事务。有意为之的（幂等的、后写覆盖是正确语义的）写进 ALLOW，
每条都要给出理由——理由本身就是复查时要读的东西。

退出码：0 干净 / 1 有未登记的裸守卫 / 2 自己空转了（没扫到东西）
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2] / "backend-go" / "internal"

# 允许"读在事务外"的 handler，每条必须写清为什么并发下也没事。
ALLOW = {
    ("driver/handler.go", "AckReminder"):
        "确认提醒是幂等的：重复确认落到同一个 acknowledged 状态，"
        "副作用只是运单时间线上多一条记录，不涉及金额和单据。",
    ("resources/actions.go", "ReminderAcknowledge"):
        "同 driver.AckReminder，是管理端的同一个动作。",
    ("finance/statement.go", "ConfirmStatement"):
        "确认对账单只改状态，不生成任何单据；两个人同时确认落到同一结果。"
        "真正动钱的是核销，那一步走事务 + SELECT … FOR UPDATE。",
    ("exceptions/actions.go", "Assign"):
        "指派只写 assignee 和 handling，重复指派结果相同，不生成单据。",
    ("exceptions/actions.go", "Handle"):
        "提交处理结论只写 resolution 和 pending_audit，不生成单据；"
        "生成应付的是关闭那一步，它已经在事务里锁行。",
    ("resources/actions.go", "PaymentResult"):
        "外部 OA/ERP 回写付款结果，语义本就是后到的结果覆盖先到的；"
        "那次读只用来补齐请求里没给的字段。",
}

FUNC = re.compile(r"\nfunc \(h \*Handler\) (\w+)\(w http\.ResponseWriter, r \*http\.Request\) \{")
STATUS_READ = re.compile(r"h\.DB\.QueryRow\(\s*ctx\s*,\s*[`\"]\s*\n?\s*SELECT\b[^`\"]*\bstatus\b", re.I)
WRITE = re.compile(r"h\.DB\.Exec\(\s*ctx\s*,\s*[`\"]\s*\n?\s*(UPDATE|INSERT|DELETE)\b", re.I)


def main() -> int:
    if not ROOT.is_dir():
        print(f"找不到 {ROOT}", file=sys.stderr)
        return 2

    scanned = 0
    naked = []           # 有裸守卫、又没登记
    still_naked = set()  # 眼下仍然是裸守卫的，用来查名单里有没有过期条目
    guarded = 0          # 开了事务的
    for path in sorted(ROOT.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        src = path.read_text(encoding="utf-8")
        rel = str(path.relative_to(ROOT))
        for m in FUNC.finditer(src):
            scanned += 1
            name = m.group(1)
            nxt = src.find("\nfunc ", m.end())
            body = src[m.end(): nxt if nxt > 0 else len(src)]
            if "h.DB.Begin(" in body:
                guarded += 1
                continue
            if STATUS_READ.search(body) and WRITE.search(body):
                still_naked.add((rel, name))
                if (rel, name) not in ALLOW:
                    naked.append((rel, name))

    # 空转防护：正则漂了（改了 handler 签名、换了 DB 字段名）会一条都匹配不到，
    # 那时候"没有发现"和"没有检查"长得一模一样。
    if scanned < 50:
        print(f"只扫到 {scanned} 个 handler —— 正则多半失配了，这次检查不作数", file=sys.stderr)
        return 2
    known = [k for k in ALLOW if (ROOT / k[0]).is_file()]
    if len(known) != len(ALLOW):
        missing = [k for k in ALLOW if not (ROOT / k[0]).is_file()]
        print(f"ALLOW 里这些文件不存在了，名单该清理：{missing}", file=sys.stderr)
        return 2
    if guarded == 0:
        print("一个开事务的 handler 都没扫到 —— 事务的写法多半变了", file=sys.stderr)
        return 2

    # 名单自检：**已经改成事务里做的，这条豁免就是过期的**。
    #
    # 过期条目比没有名单更坏：它让人以为"这里是想清楚了才不加事务的"，
    # 于是再没人去看。车载上报那两条就是被一句与事实不符的豁免理由
    # 藏了很久（写着"走设备凭据"，而它们既没有设备凭据也不在设备侧路由组上）。
    stale = [f"{k[0]} · {k[1]}" for k in ALLOW if k not in still_naked]
    if stale:
        print(f"ALLOW 里这些已经不是裸守卫了，豁免过期（共 {len(stale)} 处）：\n")
        for x in sorted(stale):
            print(f"  {x}")
        print("\n从 ALLOW 里删掉。名单一旦有一条不可信，整份名单就都不可信了。")
        return 1

    if naked:
        print(f"这些 handler 的状态守卫读在事务外，并发下等于没有守卫（共 {len(naked)} 处）：\n")
        for rel, name in naked:
            print(f"  internal/{rel} · {name}")
        print(
            "\n修法：h.DB.Begin 开事务，读那一行用 SELECT … FOR UPDATE，"
            "校验和写都放在事务里。\n"
            "如果并发下确实无害（幂等、或后写覆盖就是正确语义），"
            f"把它写进 {Path(__file__).name} 的 ALLOW 并说明理由。"
        )
        return 1

    print(f"✓ 扫了 {scanned} 个 handler（{guarded} 个开了事务），"
          f"{len(ALLOW)} 处裸守卫都已登记理由")
    return 0


if __name__ == "__main__":
    sys.exit(main())
