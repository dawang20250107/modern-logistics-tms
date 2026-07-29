#!/usr/bin/env bash
# 双栈契约比对：同一路径分别打 Go 网关(:8000) 与 Django 上游(:8001)，语义 diff。
# 用法：scripts/dev/diff.sh /api/v1/orders
set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOK=$(cat /tmp/tok.txt 2>/dev/null)
curl -s -H "Authorization: Bearer $TOK" "http://127.0.0.1:8000$1" > /tmp/go.json
curl -s -H "Authorization: Bearer $TOK" "http://127.0.0.1:8001$1" > /tmp/dj.json
python3 "$ROOT/scripts/dev/deepdiff.py" /tmp/dj.json /tmp/go.json
