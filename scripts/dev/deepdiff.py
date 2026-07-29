"""双栈响应的语义 diff。

比的是"契约是否等价"而不是字节是否相同，因此做三件归一化：
- 时间戳统一解析成时间点再比（+00:00 与 Z、微秒位数差异都不算差异）
- 整数值的浮点（1.0）与整数（1）视作相同
- 全部对不上时再做一次「集合等价」复检，用来区分「内容不同」与「仅排序不同」

用法：deepdiff.py <django.json> <go.json>
"""

import json
import re
import sys
from datetime import datetime


def norm(v):
    if isinstance(v, str):
        s = re.sub(r"(\.\d{6})\d+", r"\1", v.replace("Z", "+00:00"))
        for f in ("%Y-%m-%dT%H:%M:%S.%f%z", "%Y-%m-%dT%H:%M:%S%z"):
            try:
                return ("<ts>", datetime.strptime(s, f).timestamp())
            except Exception:
                pass
        return v
    if isinstance(v, dict):
        return {k: norm(x) for k, x in v.items()}
    if isinstance(v, list):
        return [norm(x) for x in v]
    if isinstance(v, bool):
        return v
    if isinstance(v, float) and v == int(v):
        return int(v)
    return v


def walk(a, b, path, out, limit=6):
    if len(out) >= limit:
        return
    if type(a) is not type(b) and not (isinstance(a, (int, float)) and isinstance(b, (int, float))):
        out.append((path, a, b))
        return
    if isinstance(a, dict):
        for k in sorted(set(a) | set(b)):
            walk(a.get(k), b.get(k), f"{path}.{k}", out, limit)
    elif isinstance(a, list):
        if len(a) != len(b):
            out.append((path + "[len]", len(a), len(b)))
            return
        for i, (x, y) in enumerate(zip(a, b)):
            walk(x, y, f"{path}[{i}]", out, limit)
    elif a != b:
        out.append((path, a, b))


def as_set(x):
    if not isinstance(x, list):
        return None
    return sorted(json.dumps(e, sort_keys=True, ensure_ascii=False, default=str) for e in x)


def main():
    a = norm(json.load(open(sys.argv[1]))["data"])
    b = norm(json.load(open(sys.argv[2]))["data"])
    out = []
    walk(a, b, "", out)
    if not out:
        print("OK")
        return

    same_set = None
    for key in ("items", ""):
        la = a.get(key) if key and isinstance(a, dict) else (a if isinstance(a, list) else None)
        lb = b.get(key) if key and isinstance(b, dict) else (b if isinstance(b, list) else None)
        if la is not None and lb is not None:
            same_set = as_set(la) == as_set(lb)
            break
    print(f"{len(out)} 处差异" + (f"（集合等价：{same_set}，属排序差异）" if same_set else ""))
    for p, x, y in out[:6]:
        print(f"  {p}\n    dj: {json.dumps(x, ensure_ascii=False, default=str)[:160]}"
              f"\n    go: {json.dumps(y, ensure_ascii=False, default=str)[:160]}")


if __name__ == "__main__":
    main()
