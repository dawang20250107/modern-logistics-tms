#!/usr/bin/env bash
# 从零起一套：空库 → 迁移 → 播种 → 起网关 → 登录 → 走一遍关键接口。
#
# 回答的是别的检查都回答不了的那个问题：**新客户拿到这套东西，
# 从一个空库开始，能不能跑起来。**
#   · CI 里的 migrate/seed 只证明 SQL 能执行，没起过服务、没登录过
#   · 单元测试跑在一个早就建好的库上
#   · 浏览器走查跑在开发库上，那里的数据是几十轮改动堆出来的
#
# 这一轮就抓到两个只有从零跑才看得见的问题：演示司机没有身份证号
# （司机端一个都登不进去）、演示账号没有员工档案（员工名录是空的）。
#
# 用独立的库和端口，跑完删库，不碰开发环境。
#   bash scripts/dev/coldstart.sh
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DB="${COLD_DB:-tms_cold}"
PORT="${COLD_PORT:-8010}"
export PGHOST="${PGHOST:-127.0.0.1}" PGUSER="${PGUSER:-tms}" PGPASSWORD="${PGPASSWORD:-tms}"
FAIL=0
GW_PID=""

cleanup() {
  [ -n "$GW_PID" ] && kill "$GW_PID" 2>/dev/null || true
  psql -d postgres -q -c "DROP DATABASE IF EXISTS $DB" 2>/dev/null || true
  rm -rf /tmp/cold-media
}
trap cleanup EXIT

bad() { echo "  ✗ $1"; FAIL=1; }
ok()  { echo "  ✓ $1"; }

cd "$ROOT/backend-go"
echo "── 空库 ──"
psql -d postgres -q -c "DROP DATABASE IF EXISTS $DB"
psql -d postgres -q -c "CREATE DATABASE $DB OWNER $PGUSER"
export DATABASE_URL="postgres://$PGUSER:$PGPASSWORD@$PGHOST:5432/$DB?sslmode=disable"

echo "── 迁移 ──"
go run ./cmd/migrate >/tmp/cold-migrate.log 2>&1 || { bad "迁移失败"; tail -5 /tmp/cold-migrate.log; exit 1; }
TABLES=$(psql -d "$DB" -tAc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")
[ "$TABLES" -gt 50 ] && ok "建出 $TABLES 张表" || bad "只建出 $TABLES 张表"

echo "── 播种 ──"
go run ./cmd/seed >/tmp/cold-seed.log 2>&1 || { bad "播种失败"; tail -5 /tmp/cold-seed.log; exit 1; }
ok "演示数据就绪"

echo "── 起网关 ──"
go build -o /tmp/cold-gw ./cmd/server
# 监听地址的环境变量是 GO_LISTEN_ADDR，不是 PORT
DJANGO_DEBUG=true GO_LISTEN_ADDR=":$PORT" SECRET_KEY=cold-start-dev-secret-key-0123456789 \
  MEDIA_ROOT=/tmp/cold-media DATABASE_URL="$DATABASE_URL" \
  setsid /tmp/cold-gw > /tmp/cold-gw.log 2>&1 &
GW_PID=$!
for _ in $(seq 1 40); do curl -sf -o /dev/null "http://127.0.0.1:$PORT/readyz" && break; sleep 0.3; done
if curl -sf -o /dev/null "http://127.0.0.1:$PORT/readyz"; then ok "/readyz 通"; else
  bad "网关起不来"; tail -10 /tmp/cold-gw.log; exit 1
fi

API="http://127.0.0.1:$PORT/api/v1"
echo "── 登录并走关键接口 ──"
TOK=$(curl -s -X POST "$API/auth/token" -H 'Content-Type: application/json' \
  -d '{"username":"seed_admin","password":"Demo12345!"}' \
  | python3 -c "import sys,json;print(json.load(sys.stdin).get('data',{}).get('access',''))" 2>/dev/null || true)
[ -n "$TOK" ] && ok "seed_admin 登录成功" || { bad "登录失败"; exit 1; }

for p in "/auth/me" "/orders?page_size=1" "/waybills?page_size=1" "/org/employees" \
         "/finance/statement-overview" "/analytics/dashboard" "/finance/pricing-rules?page_size=1"; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$API$p" -H "Authorization: Bearer $TOK")
  [ "$code" = "200" ] && ok "$p" || bad "$p → $code"
done

# 司机端：演示司机必须真的能登进去。
# 这一条抓到过 seed 把身份证号写成空串——而司机端登录要「手机号 + 后 6 位」，
# 缺号时后端直接拒登，于是验收时客户点开司机端，用哪个司机都是"不匹配"。
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/driver/login" \
  -H 'Content-Type: application/json' -d '{"phone":"13700000001","id_tail":"010101"}')
[ "$code" = "200" ] && ok "演示司机能登进司机端" || bad "司机端登录 → $code（演示司机档案缺身份证号？）"

# 员工名录不能是空的：那一页的主体内容和挂在员工行上的动作都靠它
n=$(curl -s "$API/org/employees" -H "Authorization: Bearer $TOK" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['total'])" 2>/dev/null || echo 0)
[ "$n" -ge 4 ] && ok "员工名录 $n 人" || bad "员工名录只有 $n 人（演示账号没有员工档案？）"

# 权限点目录：少了的话权限矩阵上没有勾选框，功能对非超管永久 403
perms=$(psql -d "$DB" -tAc 'SELECT count(*) FROM iam_permission')
[ "$perms" -ge 15 ] && ok "权限点 $perms 个" || bad "权限点只有 $perms 个"

echo
if [ "$FAIL" = 0 ]; then echo "✓ 从零起库这条路是通的"; else echo "✗ 从零起库有问题"; exit 1; fi
