#!/usr/bin/env bash
# 并跑期本地环境一键拉起：PostgreSQL + Django 上游(:8001) + Go 网关(:8000)。
#
# 放进版本库的原因：这些脚本原来散在 /tmp，容器一重建就全没了，每次都要凭记忆
# 重搭一遍环境。收官删掉 backend/ 时，本脚本里的 Django 部分一并删除。
set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PGDATA=${PGDATA:-/var/lib/postgresql/testdata}
PGBIN=$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | tail -1)

start_pg() {
  if pg_isready -h 127.0.0.1 -p 5432 -q 2>/dev/null; then echo "postgres 已在运行"; return; fi
  su postgres -c "$PGBIN/pg_ctl -D $PGDATA -l /tmp/pg.log -o '-p 5432 -k /var/run/postgresql' start" >/dev/null 2>&1
  for _ in $(seq 1 20); do pg_isready -h 127.0.0.1 -p 5432 -q 2>/dev/null && break; sleep 0.5; done
  pg_isready -h 127.0.0.1 -p 5432 -q 2>/dev/null && echo "postgres 已启动" || echo "postgres 启动失败，见 /tmp/pg.log"
}

start_django() {
  if curl -sf -o /dev/null http://127.0.0.1:8001/api/v1/auth/methods; then echo "django 已在运行"; return; fi
  (cd "$ROOT/backend" && setsid python3 manage.py runserver 127.0.0.1:8001 \
      --settings=config.settings.local_standalone --noreload > /tmp/dj.log 2>&1 &)
  for _ in $(seq 1 30); do curl -sf -o /dev/null http://127.0.0.1:8001/api/v1/auth/methods && break; sleep 0.5; done
  curl -sf -o /dev/null http://127.0.0.1:8001/api/v1/auth/methods && echo "django 已启动" || echo "django 启动失败，见 /tmp/dj.log"
}

start_gateway() {
  # 用 PID 文件精确停旧进程：按名字匹配会连带杀掉调用方自身的命令行
  local pidf=/tmp/gw.pid
  if [ -f "$pidf" ]; then
    local old; old=$(cat "$pidf")
    [ -n "$old" ] && kill "$old" 2>/dev/null
    for _ in 1 2 3 4 5; do kill -0 "$old" 2>/dev/null || break; sleep 0.3; done
    rm -f "$pidf"
  fi
  (cd "$ROOT/backend-go" && go build -o /tmp/tms-gateway ./cmd/server) || { echo "网关构建失败"; return 1; }
  setsid /tmp/tms-gateway > /tmp/gw.log 2>&1 &
  echo $! > "$pidf"
  for _ in $(seq 1 20); do curl -sf -o /dev/null http://127.0.0.1:8000/healthz && break; sleep 0.3; done
  curl -sf -o /dev/null http://127.0.0.1:8000/healthz && echo "网关已启动 (pid $(cat $pidf))" || echo "网关启动失败，见 /tmp/gw.log"
}

token() {
  curl -s -X POST -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"Admin12345!"}' \
    http://127.0.0.1:8000/api/v1/auth/token \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['access'])" > /tmp/tok.txt 2>/dev/null \
    && echo "令牌已写入 /tmp/tok.txt" || echo "取令牌失败"
}

start_pg; start_django; start_gateway; token
