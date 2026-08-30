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

  # 起之前先确认端口是空的。
  #
  # 走到这里时旧进程应该已经被 pid 文件停掉了；如果 /healthz 还有人应答，
  # 说明有个我们**管不到**的进程占着端口（上一次泄漏的、或者手工起的）。
  # 这时候接着起是没意义的：新进程 bind 失败会退出，而 /healthz 由那个野进程
  # 答 200，脚本就会报告「网关已启动」——报告的是成功，跑着的是别人。
  # 排查时看到的现象是「代码改了、重启了、行为没变」。
  if curl -sf -o /dev/null http://127.0.0.1:8000/healthz 2>/dev/null; then
    echo "网关启动中止：8000 端口已被一个不在 $pidf 里的进程占着。"
    echo "  先确认它是什么：ps -eo pid,cmd | grep '[t]ms-gateway'"
    return 1
  fi

  # 用 nohup 而不是 setsid：`setsid cmd &` 的 $! 是 setsid 自己的 pid，
  # 而 setsid 在调用方已是进程组长时会**再 fork 一次**然后退出——
  # 于是 pid 文件里记的进程立刻就死了，真正的网关跑在另一个 pid 下。
  # 后果是「停旧进程」这一步实际上什么也没停。
  MEDIA_ROOT="$ROOT/media" nohup /tmp/tms-gateway > /tmp/gw.log 2>&1 &
  local pid=$!
  echo "$pid" > "$pidf"

  # 等到「端口通」**且**「我们记的 pid 还活着」同时成立。
  # 只等端口通是不够的——第一版就是这么写的，结果健康检查被野进程秒答，
  # 循环第一轮就跳出，那时新进程还在连库、还没走到 bind，kill -0 自然是活的，
  # 断言就这么被绕过去了。这是个时序坑，不是逻辑坑。
  local ok=0
  for _ in $(seq 1 30); do
    if ! kill -0 "$pid" 2>/dev/null; then break; fi
    if curl -sf -o /dev/null http://127.0.0.1:8000/healthz 2>/dev/null; then ok=1; break; fi
    sleep 0.3
  done
  if [ "$ok" != 1 ] || ! kill -0 "$pid" 2>/dev/null; then
    echo "网关启动失败。日志最后几行："
    tail -3 /tmp/gw.log 2>/dev/null | sed 's/^/    /'
    rm -f "$pidf"
    return 1
  fi
  echo "网关已启动 (pid $pid)"
}

token() {
  curl -s -X POST -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"Admin12345!"}' \
    http://127.0.0.1:8000/api/v1/auth/token \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['access'])" > /tmp/tok.txt 2>/dev/null \
    && echo "令牌已写入 /tmp/tok.txt" || echo "取令牌失败"
}

start_pg; start_gateway; token
