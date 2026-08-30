#!/usr/bin/env python3
"""部署配置里设的每个环境变量，代码是不是真的会去读。

存在的理由：SMTP 那五个变量在 docker-compose.prod.yml 和 .env.prod.example 里
叫 TMS_SMTP_*，而代码读的是 SMTP_*。运维照着模板把五项都填好、重启，
**邮件一封也发不出去**，日志里那句警告还在要一个他配置文件里根本没有的
SMTP_HOST。设了没用比没得设更难查：配置看起来是齐的，功能就是不工作，
而没有任何一处会报错。

用法：python3 scripts/dev/env-match.py   （退出码非 0 = 有设了没用的变量）
"""
import re
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]

# 不是给网关读的，但部署配置里确实设了它。**只写实际设了的**：
# 原先这里还挂着 PGDATA / TZ / PATH / HOME / SHELL / NODE_ENV /
# COMPOSE_PROJECT_NAME / COMPOSE_FILE / VITE_API_BASE 九条，
# 而这九个名字在四份部署配置里一个都没出现过——是"顺手先写上免得以后报"
# 加进来的。名单里塞着没有对象的条目，下一个人读到只会以为
# "这些是有意豁免的"，于是名单整体不再可信。
# 加一条就得说明白它为什么不需要网关去读，而且它得真的被设了。
ALLOW = {
    "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD",  # postgres 官方镜像自己读
    "VITE_AMAP_KEY",                                      # 前端构建期由 vite 注入，不进网关
}


# 读环境变量的写法。除了 os.Getenv，仓库里还有几个包装：
# config.env / auth.envOr / orders.env / telematics.envFloat，
# 以及限流阈值的 httpx.NewThrottle("THROTTLE_X", ...) 这种第一参数形式。
# 统一按「名字里带 env 或 Getenv 的调用」加 NewThrottle 来认。
ENV_READER = re.compile(
    r'(?:\b\w*(?:[Ee]nv|Getenv)\w*|NewThrottle)\(\s*"([A-Z][A-Z_0-9]{2,})"')


def code_env_names() -> set[str]:
    """代码里真的被当环境变量读的名字。

    **原先这里有个兜底**：任何出现在 Go 源码里的全大写下划线字面量都算
    "代码知道它"。那一条把 191 个错误码（`ALREADY_SIGNED`、`CARRIER_BLOCKED`、
    `BATCH_TOO_LARGE`…）和测试夹具（`AKIDEXAMPLE`、`AWS4`）也算了进来——
    真正当环境变量读的只有 57 个，而脚本对外报的是"代码认识 246 个名字"。

    后果是这条检查基本不会红：部署配置里只要写一个名字撞上任何一个错误码，
    就当场过关。而它存在的理由正是 SMTP 那五个变量——**设了没用，
    看起来配置是齐的，功能就是不工作，没有任何一处会报错**。
    一条几乎不会红的检查，比没有这条检查更糟。

    改成按「读环境变量的调用形式」认。兜底去掉之后只多报两个
    （ALERT_SPEED_LIMIT_KMH / DEVICE_OFFLINE_MINUTES，走的是 envFloat），
    补进 ENV_READER 就对上了——也就是说兜底一直在替这两个遮掩，
    顺带替所有真问题一起遮掩。
    """
    names: set[str] = set()
    for f in (ROOT / "backend-go").rglob("*.go"):
        names |= set(ENV_READER.findall(f.read_text()))
    return names


def deploy_env_names():
    """部署配置里设置的变量（compose 的 KEY: 与 .env 的 KEY=）。"""
    found = []
    for rel in ("deploy/docker-compose.prod.yml", "deploy/docker-compose.yml",
                "deploy/.env.prod.example", "deploy/.env.example"):
        p = ROOT / rel
        if not p.exists():
            continue
        for i, line in enumerate(p.read_text().split("\n"), 1):
            if line.lstrip().startswith("#"):
                continue
            m = re.match(r'\s*([A-Z][A-Z_0-9]{2,})\s*[:=]', line)
            if m:
                found.append((m.group(1), f"{rel}:{i}"))
    return found


def main() -> int:
    known = code_env_names()
    if not known:
        print("代码里一个环境变量名都没扫到——正则失效了，这条检查正在空转", file=sys.stderr)
        return 2
    setters = deploy_env_names()
    if not setters:
        print("部署配置里一个变量都没扫到——这条检查正在空转", file=sys.stderr)
        return 2

    bad = [(n, w) for n, w in setters if n not in known and n not in ALLOW]
    for n, w in bad:
        print(f"  ✗ {n}\n      {w}  —— 部署配置里设了它，代码里没有任何地方读")

    # 名单自检。白名单的理由是"不是给网关读的"，这句话会过期：
    # 变量从部署配置里删掉了，或者网关后来真的读它了，这条豁免就成了假话。
    # 一条写错的理由能把一个真问题藏到发布之后——名单一旦有一条不可信，
    # 整份名单就都不可信了。
    names = {n for n, _ in setters}
    stale = [n for n in sorted(ALLOW) if n in known]
    gone = [n for n in sorted(ALLOW) if n not in names and n not in known]
    for n in stale:
        print(f"  ✗ {n}\n      白名单说它「不是给网关读的」，而代码里确实在读——这条豁免是假的，删掉")
    for n in gone:
        print(f"  ✗ {n}\n      白名单里留着它，而部署配置里已经没人设了——这条豁免没有对象，删掉")

    print(f"\n部署配置里的变量 {len(setters)} 个，代码认识 {len(known)} 个名字，"
          f"设了没用的 {len(bad)} 个；白名单 {len(ALLOW)} 条（过期 {len(stale)}，无对象 {len(gone)}）")
    return 1 if (bad or stale or gone) else 0


sys.exit(main())
