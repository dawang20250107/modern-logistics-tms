#!/usr/bin/env python3
"""上传物必须经过网关发出去，不能让 nginx 直接从磁盘发。

网关在 /media/ 这条路由上做了一件要紧的事：**按类型决定能不能内联**。
上传路径都用 http.DetectContentType 按内容嗅探类型，传一个 .html 上去
存下来就是 text/html；原样发出去，脚本就在应用的同源里执行。
（`X-Content-Type-Options: nosniff` 挡不住——它只禁止浏览器"猜"类型，
而这里声明出来的类型本身就是 text/html。）
最短的攻击路径是司机自助上传：只要司机令牌就能传，客服在后台点开就中招。

网关那侧已经修了：只有确定安全的类型按原类型内联，其余降成
application/octet-stream 并强制下载。

而这条防护有个**前提**：请求真的到得了网关。nginx 里
`location ~ ^/(api|healthz|readyz|media)` 是 proxy_pass 到 gateway 的，
所以现在成立。但"给静态文件加个 alias 少一跳"是很自然的优化念头，
真那么改了，防护就悄悄没了——而且不会有任何报错，
只有下一次有人上传 HTML 时才出事。

所以把这个前提写成检查。

退出码：0 过 / 1 有发现 / 2 没跑起来
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
NGINX = ROOT / "deploy" / "nginx.conf"
GATEWAY_MEDIA = ROOT / "backend-go" / "cmd" / "server" / "main.go"


def main() -> int:
    if not NGINX.is_file():
        print(f"找不到 {NGINX}", file=sys.stderr)
        return 2
    conf = NGINX.read_text(encoding="utf-8")

    # 找所有 location 块，看谁管 /media
    blocks = re.findall(r"location\s+([^\s{]+(?:\s+[^\s{]+)?)\s*\{([^}]*)\}", conf, re.S)
    if len(blocks) < 2:
        print("nginx.conf 里没扫到 location 块 —— 正则多半失配了，这次结论不作数",
              file=sys.stderr)
        return 2

    handling = []
    for pattern, body in blocks:
        # 这个 location 会不会接到 /media/xxx
        matches_media = "media" in pattern
        if not matches_media:
            continue
        proxied = "proxy_pass" in body
        served = re.search(r"\b(alias|root)\b", body) is not None
        handling.append((pattern.strip(), proxied, served))

    if not handling:
        print("nginx.conf 里没有任何 location 提到 media —— "
              "那 /media/ 会落到默认的静态文件块上，等于绕过网关的类型判断",
              file=sys.stderr)
        return 1

    bad = []
    for pattern, proxied, served in handling:
        if served:
            bad.append(f"location {pattern} 用 alias/root 直接从磁盘发文件")
        elif not proxied:
            bad.append(f"location {pattern} 既没有 proxy_pass 也没有 alias/root，落点不明")

    # 反向自检：网关那侧的类型判断还在不在。
    # 它没了的话，这条检查守的东西已经不存在，"配置对了"就成了假绿。
    if GATEWAY_MEDIA.is_file():
        gw = GATEWAY_MEDIA.read_text(encoding="utf-8")
        if "inlineSafeMedia" not in gw:
            print("网关里找不到 inlineSafeMedia —— "
                  "/media/ 的类型白名单没了，nginx 配置对不对已经无关紧要", file=sys.stderr)
            return 1
    else:
        print(f"找不到 {GATEWAY_MEDIA}", file=sys.stderr)
        return 2

    if bad:
        print("上传物没有经过网关发出去：")
        for b in bad:
            print("  " + b)
        print("\n/media/ 必须 proxy_pass 到网关。网关那侧按类型决定能不能内联——"
              "绕过去就等于把上传的 HTML 原样当网页发，脚本在应用的同源里执行。")
        return 1

    print(f"✓ /media/ 经 {len(handling)} 条 location 全部 proxy_pass 到网关，"
          "网关侧的类型白名单也在")
    return 0


if __name__ == "__main__":
    sys.exit(main())
