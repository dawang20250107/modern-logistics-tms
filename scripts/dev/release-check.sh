#!/usr/bin/env bash
# 发版闸门：把所有"机器能判的"检查跑成一条命令。
#
#   bash scripts/dev/release-check.sh          # 全跑
#   bash scripts/dev/release-check.sh --quick  # 跳过压测与备份演练（约 1 分钟）
#
# 这是打 tag 之前该跑的那一条命令。CI 已经跑了构建/测试/类型检查，
# 但 CI 跑不到的几项恰恰是最容易在发版时出事的：
#   · 生产配置能不能通过前置检查（占位密钥、CORS=*）
#   · 仓库里有没有混进真密钥
#   · 有量之后接口还撑不撑得住
#   · 备份到底能不能恢复
#
# 退出码非 0 = 不要发。每一项都会打印它到底判了什么，
# 不合格时打印怎么复现——"红了但看不出为什么红"的门禁没人会认真对待。
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
QUICK=0; [ "${1:-}" = "--quick" ] && QUICK=1

PASS=0; FAIL=0; SKIP=0
ok()   { echo "  ✓ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✗ $1"; FAIL=$((FAIL+1)); }
skip() { echo "  – $1"; SKIP=$((SKIP+1)); }
sect() { echo; echo "── $1 ──"; }

# ── 后端 ───────────────────────────────────────────────
sect "后端"
( cd backend-go && go build ./... ) >/tmp/rc-build.log 2>&1 \
  && ok "go build" || { bad "go build（见 /tmp/rc-build.log）"; }
( cd backend-go && go vet ./... ) >/tmp/rc-vet.log 2>&1 \
  && ok "go vet" || bad "go vet（见 /tmp/rc-vet.log）"
UNFMT=$( cd backend-go && gofmt -l . )
[ -z "$UNFMT" ] && ok "gofmt" || bad "gofmt 未格式化：$UNFMT"

if pg_isready -q 2>/dev/null; then
  ( cd backend-go && DATABASE_URL="${DATABASE_URL:-postgres://tms:tms@127.0.0.1:5432/tms}" \
      DJANGO_SECRET_KEY="${DJANGO_SECRET_KEY:-test-insecure-secret-min-32-bytes-long!!}" \
      go test ./... ) >/tmp/rc-test.log 2>&1 \
    && ok "go test（含需要库的鉴权矩阵/分页/共享状态用例）" \
    || bad "go test（见 /tmp/rc-test.log）"
else
  # 没库时鉴权矩阵会 t.Skip——那正是最该跑的一批，不能算通过
  skip "go test：连不上 Postgres，鉴权矩阵与分页用例会被跳过，不算数"
fi

# ── 前端 ───────────────────────────────────────────────
sect "前端"
if [ -d frontend/node_modules ]; then
  ( cd frontend && npx tsc --noEmit ) >/tmp/rc-tsc.log 2>&1 \
    && ok "tsc --noEmit" || bad "tsc（见 /tmp/rc-tsc.log）"
  ( cd frontend && npx vitest run ) >/tmp/rc-vitest.log 2>&1 \
    && ok "vitest" || bad "vitest（见 /tmp/rc-vitest.log）"
  ( cd frontend && npx vite build ) >/tmp/rc-vite.log 2>&1 \
    && ok "vite build" || bad "vite build（见 /tmp/rc-vite.log）"
  # 分页口径走查不需要起服务，纯静态扫描
  node scripts/dev/paging-audit.mjs >/tmp/rc-paging.log 2>&1 \
    && ok "分页口径（没有把「当前页条数」当总数）" \
    || { bad "分页口径走查有发现："; sed 's/^/      /' /tmp/rc-paging.log; }

  # 走查要连开发服务器。连不上时必须报"没跑"，不能报"没过"——
  # 把"跑不起来"记成失败，几次之后大家就开始无视这一项了。
  if curl -sf -o /dev/null http://127.0.0.1:5173/ 2>/dev/null; then
    node scripts/dev/ui-audit.mjs >/tmp/rc-ui.log 2>&1 \
      && ok "UI 走查（配色/间距预算）" || bad "UI 走查超预算（见 /tmp/rc-ui.log）"
    node scripts/dev/smoke-ui.mjs >/tmp/rc-smoke.log 2>&1 \
      && ok "浏览器冒烟（各页无非 2xx、无未捕获异常）" \
      || { bad "浏览器冒烟有发现："; tail -6 /tmp/rc-smoke.log | sed 's/^/      /'; }
    # 端到端业务链要连网关，前面已确认它在
    if curl -sf -o /dev/null http://127.0.0.1:8000/readyz 2>/dev/null; then
      node scripts/dev/e2e-flow.mjs >/tmp/rc-e2e.log 2>&1 \
        && ok "端到端业务链（建单→检索→详情→计数一致→翻页）" \
        || { bad "端到端业务链失败："; tail -10 /tmp/rc-e2e.log | sed 's/^/      /'; }
    else
      skip "端到端业务链：网关没起"
    fi
  else
    skip "UI 走查：前端开发服务器没起（cd frontend && npm run dev）"
  fi
else
  skip "前端：未装依赖（frontend/node_modules 不存在），先 npm ci"
fi

# ── 生产配置 ───────────────────────────────────────────
sect "生产配置前置检查"
# 用一套"看起来像生产但故意留坑"的环境跑 Preflight，确认它真的拦得住。
# 只验"好配置能过"是验不出东西的——Preflight 的价值全在拦截上。
# go run /dev/stdin 在 module 模式下会报 "outside main module"，
# 所以落成模块内的临时文件再跑，跑完删掉。
PFDIR="backend-go/internal/config/releasecheck_tmp"
mkdir -p "$PFDIR"
cat > "$PFDIR/main.go" <<'EOF'
package main

import (
	"fmt"
	"os"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/config"
)

// 每组：环境变量 + 是否期望被拦下。
// 只验"好配置能过"是验不出东西的——Preflight 的价值全在拦截上。
var cases = []struct {
	name string
	env  map[string]string
	deny bool
}{
	{"占位密钥", map[string]string{"DJANGO_SECRET_KEY": "dev-insecure-secret-change-me-min-32-bytes"}, true},
	{"密钥过短", map[string]string{"DJANGO_SECRET_KEY": "short"}, true},
	{"CORS 通配", map[string]string{"DJANGO_SECRET_KEY": "a-real-looking-production-secret-key-32b+", "DJANGO_CORS_ORIGINS": "*"}, true},
	{"合规配置", map[string]string{"DJANGO_SECRET_KEY": "a-real-looking-production-secret-key-32b+", "DJANGO_CORS_ORIGINS": "https://tms.example.com"}, false},
}

func main() {
	bad := 0
	for _, c := range cases {
		os.Clearenv()
		os.Setenv("DJANGO_DEBUG", "false") // 生产口径：Preflight 只在非 debug 下生效
		for k, v := range c.env {
			os.Setenv(k, v)
		}
		err := config.Load().Preflight()
		if got := err != nil; got != c.deny {
			want := "放行"
			if c.deny {
				want = "拦截"
			}
			fmt.Printf("FAIL %s：期望%s，实际 err=%v\n", c.name, want, err)
			bad++
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}
EOF
CHK=$( cd backend-go && go run ./internal/config/releasecheck_tmp 2>&1 )
PFRC=$?
rm -rf "$PFDIR"
[ "$PFRC" -eq 0 ] && ok "Preflight：占位密钥/短密钥/CORS 通配都拦得住，合规配置放行" \
             || bad "Preflight 行为不符预期：$CHK"

# ── 密钥泄漏 ───────────────────────────────────────────
sect "仓库里有没有混进真密钥"
# 只扫会被打包进产物的地方。测试与示例里的假密钥不算，
# 但它们必须长得一眼是假的——所以白名单是显式的，不是"含 test 就放过"。
LEAK=$(grep -rInE '(sk-[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----)' \
        --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yml' \
        --include='*.yaml' --include='*.json' --include='*.env*' \
        backend-go frontend/src deploy .github 2>/dev/null \
      | grep -v 'deploy/certs/' || true)
[ -z "$LEAK" ] && ok "未发现 API key / AWS key / 私钥" \
               || { bad "疑似密钥："; echo "$LEAK" | sed 's/^/      /'; }

# 只查生产编排。本地那份 docker-compose.yml 给缺省值是对的——
# 它就是拿来"clone 完直接 up"的，要求先配一堆变量反而是坏设计。
# 生产编排里任何一条"能跑起来的默认口令"都是问题：按文档部署的实例会共用它。
PLACE=$(grep -nE '^\s+(DJANGO_SECRET_KEY|POSTGRES_PASSWORD|DATABASE_URL):.*:-' \
          deploy/docker-compose.prod.yml 2>/dev/null || true)
[ -z "$PLACE" ] && ok "生产编排里密钥/口令都是必填（:?），没有可用的默认值" \
                || { bad "生产编排给了默认口令，按文档部署的实例会共用同一把钥匙："; echo "$PLACE" | sed 's/^/      /'; }

sect "交付材料"
for f in docs/delivery-notes.md docs/deployment.md docs/integrations.md; do
  [ -s "$f" ] && ok "$f 存在" || bad "$f 缺失或为空"
done
# 交付说明里必须写到三件会被问到的事，漏一条就会在验收现场变成纠纷
for kw in "OCR" "MEDIA_BACKEND" "adminctl"; do
  grep -q "$kw" docs/delivery-notes.md \
    && ok "交付说明覆盖了「$kw」" \
    || bad "交付说明没提到「$kw」"
done

sect "对公开放端点的限流"
# 免登录端点没有闸 = 互联网可以直接刷。/track 尤其要紧：
# 它的"密码"只有手机号后 4 位，没有闸时 60 秒能穷举完。
# 每个免登录的凭据校验端点都要有两道闸：按被猜的那个东西（单号/手机号）
# 挡定向爆破，按 IP 只对失败计数挡广撒网——只有前者会被换 IP 绕开，
# 只有后者会误伤共用出口（CGNAT、车队、企业 NAT）的正常用户。
for kv in "PublicTrack:trackByOrderThrottle:backend-go/internal/orders/public.go" \
          "PublicTrack:trackFailByIPThrottle:backend-go/internal/orders/public.go" \
          "PublicIntake:intakeThrottle:backend-go/internal/orders/public.go" \
          "DriverLogin:loginByPhoneThrottle:backend-go/internal/driver/handler.go" \
          "DriverLogin:loginFailByIPThrottle:backend-go/internal/driver/handler.go"; do
  fn="${kv%%:*}"; rest="${kv#*:}"; th="${rest%%:*}"; src="${rest##*:}"
  if grep -q "$th" "$src"; then
    ok "$fn 已挂 $th"
  else
    bad "$fn 缺少 $th —— 免登录端点没有限流"
  fi
done

sect "凭证上传"
# 回单、附件、司机证件是对账吵起来时唯一拿得出的东西。
# 这三条路径都曾经"看起来成功、其实没存"或"存了但打不开"。
if grep -q "Upload: &upl{" backend-go/internal/resources/registry.go; then
  ok "回单资源声明了文件上传"
else
  bad "ReceiptWrite 没声明 Upload —— 运单详情页传回单会 400"
fi
if grep -q "Upload: &UploadCfg{" backend-go/internal/masterdata/writecfg.go; then
  ok "司机证件资源声明了文件上传"
else
  bad "DriverCredWrite 没声明 Upload —— 资源库传证件会 400"
fi
if grep -q "h.store().Put" backend-go/internal/orders/actions.go; then
  ok "订单附件的字节会真的落盘"
else
  bad "AddAttachment 没有存文件 —— 只记了文件名"
fi
# 链接必须指向 file_display：file_url 对上传上来的文件是空串，渲染出死链
# 只看 .tsx（真正的渲染处）。第一版扫了整个 src/，结果匹配到了
# file-link.test.ts 注释里那句"href={x.file_url}"——检查被自己的说明文字绊倒，
# 报了一个不存在的问题。规则写在注释里的检查，扫描范围要绕开注释。
if grep -rn --include=*.tsx 'href={[^}]*\.file_url' frontend/src >/dev/null 2>&1; then
  bad "前端有 href 绑到 file_url —— 上传的凭证点开是死链（详见 file-link.test.ts）"
else
  ok "凭证链接都走 file_display"
fi

sect "HTTPS"
grep -q 'listen 443 ssl' deploy/nginx.conf && ok "nginx 配了 443 + TLS" || bad "nginx 没有 443"
grep -q 'Strict-Transport-Security' deploy/nginx.conf && ok "配了 HSTS" || bad "缺 HSTS"
grep -q 'return 301 https' deploy/nginx.conf && ok "80 跳 443" || bad "80 没跳 443"

# ── 需要跑起来的检查 ───────────────────────────────────
if [ "$QUICK" = 1 ]; then
  sect "压测与备份演练"
  skip "--quick：已跳过"
else
  sect "压测（需要网关在 127.0.0.1:8000）"
  if curl -sf -o /dev/null http://127.0.0.1:8000/readyz 2>/dev/null; then
    ( cd backend-go && go build -o /tmp/tms-loadtest ./cmd/loadtest ) >/dev/null 2>&1
    # 门槛按「4 核开发机 + 5 万单」定，只用来拦住数量级的退化，
    # 不是 SLA。真实 SLA 要在目标硬件上重新标定。
    /tmp/tms-loadtest -c 16 -d 15s -warmup 3s -p95 400 -maxerr 1 >/tmp/rc-load.log 2>&1 \
      && ok "压测 p95 与错误率在门槛内（详见 /tmp/rc-load.log）" \
      || { bad "压测超标（见 /tmp/rc-load.log）"; tail -6 /tmp/rc-load.log | sed 's/^/      /'; }
  else
    skip "压测：网关没起（bash scripts/dev/up.sh）"
  fi

  sect "备份恢复演练"
  if [ "${RELEASE_CHECK_DRILL:-0}" = 1 ]; then
    bash scripts/dev/backup-drill.sh >/tmp/rc-drill.log 2>&1 \
      && ok "备份→删库→恢复→取件校验全通过" \
      || { bad "演练失败（见 /tmp/rc-drill.log）"; tail -8 /tmp/rc-drill.log | sed 's/^/      /'; }
  else
    # 会 DROP DATABASE，不能默认跑——发版前在预发环境上显式开。
    skip "备份演练：会删库，需显式 RELEASE_CHECK_DRILL=1 才跑"
  fi
fi

# ── 结论 ───────────────────────────────────────────────
echo
echo "════════════════════════════════════════"
echo "通过 $PASS  失败 $FAIL  跳过 $SKIP"
if [ "$SKIP" -gt 0 ]; then
  echo "注意：跳过的项没有被验证过。发版前应当把它们补齐再跑一次，"
  echo "      「跳过」不等于「通过」。"
fi
if [ "$FAIL" -gt 0 ]; then
  echo "结论：不要发版。"
  exit 1
fi
echo "结论：机器能判的都过了。"
echo
echo "机器判不了、仍需人工确认的（脚本不该假装它能替你判）："
echo "  · 真实域名的证书链在浏览器里不报警"
echo "  · 恢复出来的库里，随便点开一张运单，回单图片能显示"
echo "  · 三处 OCR 仍是「尚未接入实现」——已写进 docs/delivery-notes.md，"
echo "    交付/验收前请与客户当面对齐这一条"
echo "  · 多副本部署必须把 MEDIA_BACKEND 改成 s3——默认的 local 是写容器本地盘，"
echo "    多副本下会间歇性丢文件，而且预发（单副本）复现不出来"
echo "  · S3 那条实现的签名没有对着真实端点验过（本机没有凭据）。"
echo "    首次接入请先在预发用测试桶跑一遍上传与读取，确认签名被接受"
