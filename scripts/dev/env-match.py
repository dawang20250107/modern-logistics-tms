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

# 不是给网关读的：数据库容器自己的初始化变量、compose/shell 内置、前端构建期变量。
# 白名单要写清楚为什么，否则下一个人只会往里加名字。
ALLOW = {
    "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD",  # postgres 官方镜像自己读
    "PGDATA", "TZ", "PATH", "HOME", "SHELL",              # 容器/系统内置
    "COMPOSE_PROJECT_NAME", "COMPOSE_FILE",
    "VITE_API_BASE", "VITE_AMAP_KEY",                     # 前端构建期，由 vite 注入
    "NODE_ENV",
}


def code_env_names() -> set[str]:
    """代码里所有可能的环境变量名来源。

    不能只认 os.Getenv("X")：限流的阈值是 httpx.NewThrottle("THROTTLE_X", ...)
    这种第一参数形式。漏掉这类会把好的报成坏的——误报比漏报更快让人放弃这个脚本。
    """
    names: set[str] = set()
    for f in (ROOT / "backend-go").rglob("*.go"):
        src = f.read_text()
        names |= set(re.findall(r'(?:Getenv|env|envOr)\(\s*"([A-Z][A-Z_0-9]{2,})"', src))
        names |= set(re.findall(r'NewThrottle\(\s*"([A-Z][A-Z_0-9]{2,})"', src))
        # 兜底：任何出现在 Go 源码里的全大写下划线字符串字面量都算"代码知道它"
        names |= set(re.findall(r'"([A-Z][A-Z_0-9]{3,})"', src))
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
    print(f"\n部署配置里的变量 {len(setters)} 个，代码认识 {len(known)} 个名字，设了没用的 {len(bad)} 个")
    return 1 if bad else 0


sys.exit(main())
