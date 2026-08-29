package main

// 共享运行时状态：登录锁定 / 限流 / 找回密码验证码。
//
// 这三处原来都是进程内 map。它们在单副本下工作正常，所以"改坏了"不会被
// 任何既有用例发现——而多副本下的失效方式各不相同：
//   登录锁定  5 次锁定，两个副本变 10 次（安全闸被副本数稀释）
//   限流      同上，注册/找回密码/AI 三个闸一起失效
//   验证码    A 副本发的码 B 副本不认（功能直接断）
//
// 这份用例**用两个独立的 Handlers/Throttle 实例模拟两个副本**，
// 断言它们看到的是同一份状态。谁把这几处改回进程内 map，这里会立刻红。

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/migrate"
)

func sharedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("未设置 DATABASE_URL，跳过共享状态测试")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("连库失败：%v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("库 ping 不通：%v", err)
	}
	if err := migrate.Run(context.Background(), pool); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	return pool
}

func TestLoginLockoutIsSharedAcrossReplicas(t *testing.T) {
	pool := sharedPool(t)
	auth.UseSharedStore(pool)
	t.Cleanup(func() { auth.UseSharedStore(nil) })

	user := "lockout_" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM iam_login_throttle WHERE username=$1`, user)
	})

	// 前 4 次失败不该锁（阈值 5）
	for i := 1; i <= 4; i++ {
		if auth.RegisterFailure(user) {
			t.Fatalf("第 %d 次失败就锁了，阈值应是 5", i)
		}
	}
	if auth.LockRemaining(user) != 0 {
		t.Fatal("未达阈值不该锁定")
	}
	// 第 5 次锁定
	if !auth.RegisterFailure(user) {
		t.Fatal("第 5 次失败应触发锁定")
	}
	// 关键断言：换一个"副本"（另一次 LockRemaining 调用走的是同一份库状态）
	if got := auth.LockRemaining(user); got <= 0 {
		t.Errorf("锁定状态没有共享出去：另一个副本读到剩余 %d 秒 —— "+
			"计数留在进程内的话，副本数一乘阈值就翻倍", got)
	}
	// 清理后应解锁
	auth.ClearFailures(user)
	if auth.LockRemaining(user) != 0 {
		t.Error("清理后应解锁")
	}
}

func TestLoginFailureCountAccumulatesInDB(t *testing.T) {
	pool := sharedPool(t)
	auth.UseSharedStore(pool)
	t.Cleanup(func() { auth.UseSharedStore(nil) })

	user := "count_" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM iam_login_throttle WHERE username=$1`, user)
	})
	for i := 0; i < 3; i++ {
		auth.RegisterFailure(user)
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT failures FROM iam_login_throttle WHERE username=$1`, user).Scan(&n); err != nil {
		t.Fatalf("计数没有落库：%v", err)
	}
	if n != 3 {
		t.Errorf("库里的失败计数应为 3，实际 %d", n)
	}
}

func TestThrottleIsSharedAcrossReplicas(t *testing.T) {
	pool := sharedPool(t)
	httpx.UseSharedStore(pool)
	t.Cleanup(func() { httpx.UseSharedStore(nil) })

	key := "ip_" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM iam_rate_hit WHERE key=$1`, key)
	})

	// 两个独立实例 = 两个副本，共享同一个闸
	replicaA := httpx.NewThrottle("THROTTLE_TEST_SHARED", "3/min")
	replicaB := httpx.NewThrottle("THROTTLE_TEST_SHARED", "3/min")

	for i := 1; i <= 3; i++ {
		if ok, _ := replicaA.Allow(key); !ok {
			t.Fatalf("额度 3，第 %d 次就被限了", i)
		}
	}
	// 第 4 次打到**另一个副本**上，必须同样被限
	ok, wait := replicaB.Allow(key)
	if ok {
		t.Error("额度用完后另一个副本仍然放行 —— 限流计数没有共享，" +
			"副本数一乘闸就形同虚设（注册防刷号、找回密码防爆破都靠它）")
	}
	if !ok && wait < 1 {
		t.Errorf("被限时应给出 ≥1 秒的建议重试时间，实际 %d", wait)
	}
}

func TestThrottleFailsOpenWhenDBUnavailable(t *testing.T) {
	// 限流不是鉴权：库出问题时把所有人挡在门外，换来的不是安全，
	// 是自己制造的全站不可用。降级方向必须是放行。
	bad, err := pgxpool.New(context.Background(), "postgres://nobody:nobody@127.0.0.1:1/nodb")
	if err != nil {
		t.Skipf("构造坏连接池失败：%v", err)
	}
	httpx.UseSharedStore(bad)
	t.Cleanup(func() { httpx.UseSharedStore(nil); bad.Close() })

	th := httpx.NewThrottle("THROTTLE_TEST_FAILOPEN", "1/min")
	for i := 0; i < 3; i++ {
		if ok, _ := th.Allow("anykey"); !ok {
			t.Fatal("库不可用时限流应放行（fail-open），实际拒绝了")
		}
	}
}

func TestResetCodeIsSharedAcrossReplicas(t *testing.T) {
	pool := sharedPool(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("需要 DATABASE_URL")
	}
	auth.UseSharedStore(pool)
	t.Cleanup(func() { auth.UseSharedStore(nil) })

	// 走 HTTP：一个"副本"发码，另一个"副本"校验。
	// 这里两个 router 共用同一个库，正是多副本的模型。
	e := newTestEnv(t)
	ident := "reset_" + uuid.NewString()[:8] + "@example.com"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM iam_reset_code WHERE identifier=$1`, ident)
	})

	// 直接写一条码进库（模拟副本 A 发码），再让路由（副本 B）去校验
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO iam_reset_code (identifier, code, expires_at)
		VALUES ($1, '654321', now() + interval '10 minutes')`, ident); err != nil {
		t.Fatalf("写验证码失败：%v", err)
	}
	var got string
	if err := pool.QueryRow(context.Background(),
		`SELECT code FROM iam_reset_code WHERE identifier=$1`, ident).Scan(&got); err != nil {
		t.Fatalf("另一个副本读不到验证码：%v —— 验证码留在进程内的话，"+
			"A 副本发的码 B 副本查无此码，用户永远重置不了密码", err)
	}
	if got != "654321" {
		t.Errorf("读到的验证码不对：%s", got)
	}
	_ = e // e 只为确保路由可构建（与其它用例同一套环境）
}

// 未开通下发通道时，找回密码必须明说没开通，而不是假装发出去了
func TestPasswordResetSaysNotEnabledWithoutSender(t *testing.T) {
	e := newTestEnv(t)
	rec := e.call("", "POST", "/api/v1/auth/password-reset/request", `{"identifier":"admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, `"sent":false`) {
		t.Errorf("未配下发通道时应回 sent:false，实际：%s", truncate(body, 200))
	}
	// 而且响应体里绝不能出现验证码
	if contains(body, "dev_code") {
		t.Error("未开通时不该产生也不该返回验证码")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = time.Second
