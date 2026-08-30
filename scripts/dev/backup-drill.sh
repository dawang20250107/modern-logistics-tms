#!/usr/bin/env bash
# 备份恢复演练：备份 → 模拟灾难 → 恢复 → 校验。
#
#   bash scripts/dev/backup-drill.sh          # 本地开发库上跑一遍
#   PGHOST=... PGUSER=... bash ... --keep     # 只备份不删库（用于生产例行备份）
#
# 为什么要有它：没演练过的备份等于没有备份。而演练最容易漏的一步是
# **库和媒体是不是同一时刻的**——只恢复库的话，对账单还在，
# 但作为凭证的回单照片指向了不存在的文件，账对不上还拿不出证据。
# 所以最后一步一定要访问一个真实的媒体文件，不是只数表行数。
#
# 危险：不带 --keep 时会 DROP DATABASE。只在你确定可以丢的库上跑。
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KEEP=0; [ "${1:-}" = "--keep" ] && KEEP=1
: "${PGHOST:=127.0.0.1}"; : "${PGUSER:=tms}"; : "${PGPASSWORD:=tms}"; : "${PGDATABASE:=tms}"
export PGHOST PGUSER PGPASSWORD
OUT="${BACKUP_DIR:-/tmp/tms-backup}"; mkdir -p "$OUT"
STAMP=$(date +%F-%H%M%S)
MEDIA="${MEDIA_ROOT:-$ROOT/media}"

q() { psql -d "$PGDATABASE" -tAc "$1"; }
counts() { q "SELECT 'orders='||(SELECT count(*) FROM ops_order)
  ||' waybills='||(SELECT count(*) FROM ops_waybill)
  ||' statements='||(SELECT count(*) FROM fin_statement)
  ||' users='||(SELECT count(*) FROM accounts_user)
  ||' tables='||(SELECT count(*) FROM information_schema.tables WHERE table_schema='public')"; }

# 停网关：按 /tmp/gw.pid 停。
# 起初这里写的是 pkill -f tms-gateway——它把脚本自己的进程组也匹配上杀了，
# 演练跑到一半自尽，退出码 144。按名字杀进程永远有这个风险。
stop_gateway() {
  local pidf=/tmp/gw.pid old
  [ -f "$pidf" ] || return 0
  old=$(cat "$pidf"); [ -n "$old" ] || return 0
  kill "$old" 2>/dev/null || true
  for _ in 1 2 3 4 5; do kill -0 "$old" 2>/dev/null || break; sleep 0.3; done
  rm -f "$pidf"
}

start_gateway() {
  (cd "$ROOT/backend-go" && go build -o /tmp/tms-gateway ./cmd/server) || { echo "  ✗ 网关构建失败"; return 1; }
  MEDIA_ROOT="$MEDIA" setsid /tmp/tms-gateway > /tmp/gw.log 2>&1 &
  echo $! > /tmp/gw.pid
  for _ in $(seq 1 30); do curl -sf -o /dev/null http://127.0.0.1:8000/readyz && return 0; sleep 0.3; done
  echo "  ✗ 网关起不来，见 /tmp/gw.log"; return 1
}

echo "── 基线 ──"; BEFORE=$(counts); echo "  $BEFORE"
MEDIA_BEFORE=$(find "$MEDIA" -type f 2>/dev/null | wc -l); echo "  媒体文件 $MEDIA_BEFORE 个"

echo "── 备份 ──"
pg_dump -d "$PGDATABASE" -Fc > "$OUT/db-$STAMP.dump"
tar czf "$OUT/media-$STAMP.tar.gz" -C "$MEDIA" . 2>/dev/null || tar czf "$OUT/media-$STAMP.tar.gz" --files-from /dev/null
echo "  $OUT/db-$STAMP.dump ($(du -h "$OUT/db-$STAMP.dump"|cut -f1))"
echo "  $OUT/media-$STAMP.tar.gz ($(du -h "$OUT/media-$STAMP.tar.gz"|cut -f1))"

if [ "$KEEP" = 1 ]; then echo "── --keep：只备份，不演练恢复 ──"; exit 0; fi

echo "── 模拟灾难（DROP DATABASE + 清空媒体）──"
# 演练要把网关停掉（不停的话连接会挡住 DROP DATABASE），但**停之前它在不在**
# 决定了跑完要不要把它起回来。原先只有"媒体非空"那一条分支会 start_gateway，
# 媒体目录为空时演练结束就把网关留在停着的状态，之后同一台机器上再跑
# release-check，浏览器走查会全部被打回登录页——而它报的是"UI 走查超预算"，
# 一条根本不存在的问题。演练可以借用环境，但必须还回去。
GW_WAS_UP=0
curl -sf -o /dev/null http://127.0.0.1:8000/readyz 2>/dev/null && GW_WAS_UP=1
restore_gateway() {
  [ "$GW_WAS_UP" = 1 ] || return 0
  curl -sf -o /dev/null http://127.0.0.1:8000/readyz 2>/dev/null && return 0
  echo "── 还原环境：把演练前就在跑的网关起回来 ──"
  start_gateway || echo "  ⚠ 网关没能起回来，后续依赖它的检查会记「没跑」"
}
trap restore_gateway EXIT
stop_gateway   # 见上：按 PID 停，不按名字杀
psql -d postgres -q -c "DROP DATABASE IF EXISTS $PGDATABASE"
psql -d postgres -q -c "CREATE DATABASE $PGDATABASE OWNER $PGUSER"
rm -rf "${MEDIA:?}"/* 2>/dev/null || true
echo "  空库表数 $(q "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")，媒体 $(find "$MEDIA" -type f|wc -l) 个"

echo "── 恢复 ──"
pg_restore -d "$PGDATABASE" --clean --if-exists "$OUT/db-$STAMP.dump" 2>"$OUT/restore-$STAMP.err" || true
ERRS=$(grep -cE '^pg_restore: error' "$OUT/restore-$STAMP.err" || true)
tar xzf "$OUT/media-$STAMP.tar.gz" -C "$MEDIA"
echo "  pg_restore 报错 $ERRS 行"

echo "── 校验 ──"
AFTER=$(counts); MEDIA_AFTER=$(find "$MEDIA" -type f|wc -l)
echo "  恢复前 $BEFORE"
echo "  恢复后 $AFTER"
FAIL=0
[ "$BEFORE" = "$AFTER" ] || { echo "  ✗ 行数对不上"; FAIL=1; }
[ "$MEDIA_BEFORE" = "$MEDIA_AFTER" ] || { echo "  ✗ 媒体文件数对不上（$MEDIA_BEFORE → $MEDIA_AFTER）"; FAIL=1; }
[ "$ERRS" = "0" ] || { echo "  ✗ pg_restore 有报错，见 $OUT/restore-$STAMP.err"; FAIL=1; }

# 库与媒体是否同一时刻：只数行数证明不了。必须真的起服务、真的取一个媒体文件。
# 库恢复到 T1、媒体恢复到 T2 的话，中间那段单据会指向不存在的文件——
# 表还是满的，行数还是对的，但凭证已经没了。这一步才是演练的意义。
# 用 -print -quit 让 find 自己停，不要 `find | head -1`。
# 后者在 `set -o pipefail` 下是一颗定时炸弹：head 读到第一行就关管道，
# find 吃到 SIGPIPE 死掉，整条流水线的状态变成 141，脚本就地退出——
# 而且**文件少的时候不会发生**（find 还没来得及往下写就写完了）。
# 演练一路正常，最后报「演练失败」，日志停在校验那一行什么也不说；
# 媒体目录攒到 145 个文件那天才第一次复现。
# 又是一条"失败原因说的不是真的"：备份和恢复其实全都是好的。
SAMPLE=$(cd "$MEDIA" && find . -type f -print -quit | sed 's|^\./||')
if [ -z "$SAMPLE" ]; then
  echo "  ⚠ 媒体目录为空，跳过端到端取件校验（这次演练没覆盖到凭证）"
else
  start_gateway || FAIL=1
  CODE=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:8000/media/$SAMPLE" || echo 000)
  if [ "$CODE" = "200" ]; then
    echo "  ✓ 取件校验 /media/$SAMPLE → 200"
  else
    echo "  ✗ 取件校验 /media/$SAMPLE → $CODE（库恢复了，凭证取不出来）"; FAIL=1
  fi
fi

if [ "$FAIL" = 0 ]; then
  echo "  ✓ 库、媒体、以及两者的一致性均已验证"
else
  exit 1
fi
