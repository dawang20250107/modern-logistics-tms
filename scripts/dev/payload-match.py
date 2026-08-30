#!/usr/bin/env python3
"""前端 POST/PATCH 的每个字段名，后端是不是真的会去取。

route-match.py 只比路径和方法，比不到 body 里面。而发布前最要命的那个 bug
恰恰在 body 里：对账中心的「生成」发的是 period_start / period_end，
后端结构体上写的是 `json:"start"` / `json:"end"`——用户明明选了账期，
后端却报「start 与 end 必填」。同一个请求里还有个 external_total 传的是
数字 0 而结构体是 string，**整个请求体解不开**，报错变成
「请求体不是合法 JSON」，排查方向彻底被带偏。

结果是对账中心第一步就走不通，而路径和方法都是对的——
route-match 看不见这一类。

判据很粗但有效：前端发出去的某个键，如果在整个 Go 源码里**一次都没出现过**
（不是 json tag、不是 body["k"]、也不是任何取值助手的参数），
那它一定没人读。反过来不成立——出现过不代表被**这个** handler 读到——
所以这条只报"肯定没人读的"，不假装能证明契约完全一致。

它抓得住的是**名字**对不上（period_start vs start），抓不住**类型**对不上
（external_total 前端发数字、后端结构体是 string）。后者那一半只有真发一次
请求才看得见——那是 write-paths.mjs 和用例的活。一个检查该说清自己看不见什么。

用法：python3 scripts/dev/payload-match.py   （退出码非 0 = 有没人读的字段）
"""
import re
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]

# 键名里出现的这些不是业务字段，是 JS 语法/局部结构
SKIP = {"headers", "method", "body", "signal", "params"}


def backend_known() -> set[str]:
    """后端可能读到某个键的所有写法。

    漏掉一种写法就会把好的报成坏的。第一版只认 json tag 和 body["k"]，
    结果 4 处全是误报——它们分别走 decOf / strField / boolField 这些助手。
    误报比漏报更快让人放弃一个脚本，所以这里宁可宽一点。
    """
    known: set[str] = set()
    for f in (ROOT / "backend-go").rglob("*.go"):
        s = f.read_text()
        known |= set(re.findall(r'json:"([a-z_0-9]+)', s))
        known |= set(re.findall(r'body\["([a-z_0-9]+)"\]', s))
        # str(body,"k") / decOf(body,"k","alt") / strField(body,"k") / boolField(body,"k",…)
        known |= set(re.findall(r'\w*(?:str|Str|dec|Dec|bool|Bool|int|Int|Field)\w*\(\s*\w*body\w*,\s*"([a-z_0-9]+)"', s))
        known |= set(re.findall(r',\s*"([a-z_0-9]+)"\s*[,)]', s))   # 助手的第二/第三个字面量参数
        known |= set(re.findall(r'"([a-z_0-9]{2,})":\s*\{', s))     # 资源配置里的字段声明
        known |= set(re.findall(r'FormValue\("([a-z_0-9]+)"\)', s))
        known |= set(re.findall(r'\.Get\("([a-z_0-9]+)"\)', s))
    return known


def frontend_payloads():
    """apiPost/apiPatch/apiPut(path, { ... }) 的顶层键。"""
    out = []
    for f in sorted((ROOT / "frontend/src").rglob("*.ts*")):
        s = f.read_text()
        for m in re.finditer(r'api(?:Post|Patch|Put)\s*(?:<[^>]*>)?\s*\(\s*[`"\']([^`"\']*)[`"\']\s*,\s*\{', s):
            start = m.end() - 1
            depth, i = 0, start
            while i < len(s):
                if s[i] == "{":
                    depth += 1
                elif s[i] == "}":
                    depth -= 1
                    if depth == 0:
                        break
                i += 1
            obj = s[start:i + 1]
            # 只取顶层键：嵌套对象里的键归它自己的接口管
            keys = []
            depth = 0
            for mm in re.finditer(r'[{}]|(?:^|[\s{,])([a-z_][a-z_0-9]*)\s*:', obj):
                tok = mm.group(0).strip()
                if tok == "{":
                    depth += 1
                elif tok == "}":
                    depth -= 1
                elif depth == 1 and mm.group(1):
                    keys.append(mm.group(1))
            out.append((m.group(1), keys, f"{f.relative_to(ROOT)}:{s[:m.start()].count(chr(10)) + 1}"))
    return out


def main() -> int:
    known = backend_known()
    calls = frontend_payloads()
    if not known or not calls:
        print("一边扫成了空——正则失效了，这条检查正在空转", file=sys.stderr)
        return 2

    bad = []
    for path, keys, where in calls:
        for k in keys:
            if k not in known and k not in SKIP:
                bad.append((path, k, where))
    for p, k, w in bad:
        print(f"  ✗ {k}  发往 {p}\n      {w}  —— 这个键名在后端源码里一次都没出现过")
    print(f"\n带 body 的调用点 {len(calls)} 处，后端从没提过的字段 {len(bad)} 个")
    return 1 if bad else 0


sys.exit(main())
