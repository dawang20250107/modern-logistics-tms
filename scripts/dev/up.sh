#!/usr/bin/env bash
# 本地环境一键拉起：PostgreSQL + Go 网关(:8000)。
#
# 放进版本库的原因：这些脚本原来散在 /tmp，容器一重建就全没了，每次都要凭记忆
# 重搭一遍环境。
set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PGDATA=${PGDATA:-/var/lib/postgresql/testdata}
PGBIN=$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | tail -1)

start_pg() {
  if pg_isready -h 127.0.0.1 -p 5432 -q 2>/dev/null; then echo "postgres 已在运行"; return; fi
  # 容器重建后 PGDATA 会整个消失，脚本原先直接 pg_ctl start 然后报"启动失败"，
  # 每次都要手工 initdb + 建角色建库。既然这脚本的卖点就是"一条命令拉起全栈"，
  # 空目录就自己初始化——从零到能用是它该负责的事。
  if [ ! -s "$PGDATA/PG_VERSION" ]; then
    echo "PGDATA 为空，初始化数据库…"
    mkdir -p "$PGDATA" /var/run/postgresql
    chown -R postgres:postgres "$PGDATA" /var/run/postgresql 2>/dev/null || true
    su postgres -c "$PGBIN/initdb -D $PGDATA -U postgres --encoding=UTF8 --locale=C" >/dev/null 2>&1
    su postgres -c "$PGBIN/pg_ctl -D $PGDATA -l /tmp/pg.log -o '-p 5432 -k /var/run/postgresql' start" >/dev/null 2>&1
    for _ in $(seq 1 20); do pg_isready -h 127.0.0.1 -p 5432 -q 2>/dev/null && break; sleep 0.5; done
    su postgres -c "psql -q -c \"CREATE ROLE tms LOGIN PASSWORD 'tms' SUPERUSER\"" >/dev/null 2>&1
    su postgres -c "psql -q -c 'CREATE DATABASE tms OWNER tms'" >/dev/null 2>&1
    echo "数据库已初始化（角色/库均为 tms）"
    return
  fi
  su postgres -c "$PGBIN/pg_ctl -D $PGDATA -l /tmp/pg.log -o '-p 5432 -k /var/run/postgresql' start" >/dev/null 2>&1
  for _ in $(seq 1 20); do pg_isready -h 127.0.0.1 -p 5432 -q 2>/dev/null && break; sleep 0.5; done
  pg_isready -h 127.0.0.1 -p 5432 -q 2>/dev/null && echo "postgres 已启动" || echo "postgres 启动失败，见 /tmp/pg.log"
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
  MEDIA_ROOT="$ROOT/media" setsid /tmp/tms-gateway > /tmp/gw.log 2>&1 &
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

start_pg; start_gateway; token
