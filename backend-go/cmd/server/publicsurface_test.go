package main

// 对整个互联网开放的那几个端点。
//
// 发布前专门过了一遍「不用登录就能打到的面」，共 7 个业务端点：
//   · /driver/{login,tasks,checkin,credentials,reminders/{id}/ack} —— 实测全部 401，把关是好的
//   · /track            —— 公开查单
//   · /public/orders    —— 客户自助下单
//
// 后两个有问题，这一组钉的就是修复。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// mkTrackableOrder 造一张能被公开查单查到的订单，返回单号与联系电话。
func (e *testEnv) mkTrackableOrder() (orderNo, phone string) {
	e.t.Helper()
	orderNo = "TRK" + uuid.NewString()[:10]
	phone = "138" + fmt.Sprintf("%08d", uuid.New().ID()%100000000)
	id := uuid.NewString()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO ops_order (id, created_at, updated_at, order_no, source, status, remark,
		  cargo_desc, cargo_quantity, cargo_volume_cbm, cargo_weight_ton, channel,
		  contact_name, contact_phone, destination, origin, parse_meta, raw_text,
		  business_type, cargo_value, delivery_address, delivery_contact_name,
		  delivery_contact_phone, is_deleted, is_hazardous, package_type, pickup_address,
		  pickup_contact_name, pickup_contact_phone, priority, quoted_amount, settlement_type,
		  source_type, temperature_range, sla_status, approval_remark, approval_status,
		  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term)
		VALUES ($1::uuid, now(), now(), $2, 'cs', 'pooled', '', '测试货', 1, 1, 1, 'cs',
		  '张三', $3, '上海', '杭州', '{}'::jsonb, '', 'ftl', 0, '收货地址', '李四', '13900000000',
		  false, false, '纸箱', '发货地址', '王五', '13700000000', 'normal', 0, 'monthly',
		  'enterprise', '', 'pending', '', 'none', '', 0, 'none', 'consignor', 'prepaid')`,
		id, orderNo, phone); err != nil {
		e.t.Fatalf("造订单失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM ops_order WHERE id=$1::uuid`, id)
	})
	return orderNo, phone
}

func (e *testEnv) track(orderNo, phone string) int {
	e.t.Helper()
	// 参数必须转义。注入形状的取值（' OR 1=1--）里带空格，
	// 原样拼进 URL 会让 httptest.NewRequest 把它当成畸形的请求行而 panic——
	// 那是测试自己炸了，不是产品挡住了攻击，两者别混为一谈。
	return e.call("", "GET", "/api/v1/track?order_no="+url.QueryEscape(orderNo)+
		"&phone="+url.QueryEscape(phone), "").Code
}

// TestPublicTrackNeedsBothParams 单号和手机号缺一不可。
// 只凭单号就能查的话，顺序单号等于把全系统的发货信息公开了。
func TestPublicTrackNeedsBothParams(t *testing.T) {
	e := newTestEnv(t)
	no, phone := e.mkTrackableOrder()
	for _, c := range []struct{ name, q string }{
		{"都不给", ""},
		{"只给单号", "?order_no=" + no},
		{"只给手机", "?phone=" + phone},
	} {
		if got := e.call("", "GET", "/api/v1/track"+c.q, "").Code; got != http.StatusBadRequest {
			t.Errorf("%s → %d，期望 400", c.name, got)
		}
	}
	if got := e.track(no, phone); got != http.StatusOK {
		t.Errorf("都给了 → %d，期望 200", got)
	}
}

// TestPublicTrackRejectsWrongPhoneAndWildcards 手机号不对、或想用通配符绕过，都要 404。
func TestPublicTrackRejectsWrongPhoneAndWildcards(t *testing.T) {
	e := newTestEnv(t)
	no, _ := e.mkTrackableOrder()
	for _, phone := range []string{
		"13900000001", // 不是这张单的号
		"%",           // LIKE 通配：SQL 拼错的话这个会命中
		"%25",
		"_",
		"' OR 1=1--",
	} {
		if got := e.track(no, phone); got == http.StatusOK {
			t.Errorf("手机号 %q 竟然查到了单——通配符或注入没被参数化挡住", phone)
		}
	}
}

// TestPublicTrackThrottlesPerOrderNo 同一个单号被反复试，必须被挡下来。
//
// 这条是这一组里最要紧的。端点免登录、对整个互联网开放，而它的"密码"
// 只是手机号后 4 位（SQL 允许 >=4 位后缀匹配，是有意的可用性取舍——
// 客户常只记得后四位），订单号又是顺序可猜的。
//
// 实测过：没有闸的时候穷举 10000 种四位组合只要 **60 秒**，
// 也就是任何人都能把全系统的起止地、状态、时间线爬走。
// 对物流公司来说那是把客户的发货规律直接交给同行。
//
// 闸挂在**订单号**上而不是只挂在 IP 上：被反复试的是那个单号，
// 换多少 IP 都绕不开。跟登录锁定按用户名而非 IP 是同一个道理。
func TestPublicTrackThrottlesPerOrderNo(t *testing.T) {
	e := newTestEnv(t)
	no, _ := e.mkTrackableOrder()

	var throttledAt = -1
	for i := 0; i < 30; i++ {
		got := e.track(no, fmt.Sprintf("%04d", i)) // 逐个试四位后缀
		if got == http.StatusTooManyRequests {
			throttledAt = i
			break
		}
	}
	if throttledAt < 0 {
		t.Fatal("同一单号连试 30 次都没被限流 —— 四位后缀可以在一分钟内被穷举完")
	}
	if throttledAt == 0 {
		t.Error("第一次尝试就被限流了，正常客户查单会被误伤")
	}
	t.Logf("第 %d 次尝试后开始限流", throttledAt+1)
}

// TestPublicTrackThrottleDoesNotBlockOtherOrders 限流是按单号的，不能误伤别的客户。
//
// 少了这一条，把闸做成全局的也能让上面那条通过——那样一个人被暴破
// 会导致所有客户都查不了单，等于把安全修复变成了拒绝服务。
func TestPublicTrackThrottleDoesNotBlockOtherOrders(t *testing.T) {
	e := newTestEnv(t)
	victim, _ := e.mkTrackableOrder()
	other, otherPhone := e.mkTrackableOrder()

	// 把 victim 那个单号打到限流
	for i := 0; i < 30; i++ {
		if e.track(victim, fmt.Sprintf("%04d", i)) == http.StatusTooManyRequests {
			break
		}
	}
	if got := e.track(victim, "9999"); got != http.StatusTooManyRequests {
		t.Fatalf("victim 单号没被限住（%d），这条用例的前提不成立", got)
	}
	// 另一个客户必须照常能查。
	//
	// 这一条抓到过两个不同的坏法：闸做成全局的（一个人被暴破就锁全场），
	// 以及按 IP 无差别限流（国内大量用户共用出口 IP，正常客户会被误伤）。
	// 后者是这条用例真正逼出来的设计——按 IP 只对**失败**计数。
	if got := e.track(other, otherPhone); got != http.StatusOK {
		t.Errorf("另一个订单被连带挡住了（%d）。可能是闸做成了全局的，"+
			"也可能是按 IP 无差别限流——后者在 NAT/CGNAT 下会让"+
			"共用出口的正常客户全都查不了单", got)
	}
}

// TestPublicTrackSuccessDoesNotConsumeIPBudget 查得到的请求不能消耗 IP 配额。
//
// 这是上一条背后的机制。国内共用出口 IP 是常态（企业 NAT、运营商 CGNAT），
// 按 IP 无差别限流的话，一个出口后面几百个客户很快就把配额用光。
// 只对失败计数，正常客户就永远不会被误伤。
func TestPublicTrackSuccessDoesNotConsumeIPBudget(t *testing.T) {
	e := newTestEnv(t)
	// 40 次成功查询（远超按 IP 的失败配额 20/min），全部来自同一个"IP"
	for i := 0; i < 40; i++ {
		no, phone := e.mkTrackableOrder()
		if got := e.track(no, phone); got != http.StatusOK {
			t.Fatalf("第 %d 次成功查询变成了 %d —— 成功也在消耗 IP 配额，"+
				"共用出口的客户会被连带挡住", i+1, got)
		}
	}
}

// TestPublicIntakeIsThrottled 免登录建单要挡住灌水。
// 不挡的话任何人都能把客服工作台填满垃圾单，而客服分不出哪些是真的。
func TestPublicIntakeIsThrottled(t *testing.T) {
	e := newTestEnv(t)
	throttled := false
	for i := 0; i < 20; i++ {
		if e.call("", "POST", "/api/v1/public/orders", `{}`).Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Error("公开建单连打 20 次都没被限流 —— 客服工作台可以被灌满")
	}
}

// TestDriverEndpointsRequireDriverToken 司机端那几个免走 RequireAuth 的路由，
// 必须各自校验司机令牌。它们不在主鉴权中间件下面，很容易被误以为是公开的。
func TestDriverEndpointsRequireDriverToken(t *testing.T) {
	e := newTestEnv(t)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/driver/tasks", ""},
		{"POST", "/api/v1/driver/checkin", `{}`},
		{"POST", "/api/v1/driver/reminders/00000000-0000-0000-0000-000000000000/ack", `{}`},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			if got := e.call("", c.method, c.path, c.body).Code; got != http.StatusUnauthorized {
				t.Errorf("不带令牌 → %d，期望 401", got)
			}
			if got := e.call("forged.token.here", c.method, c.path, c.body).Code; got != http.StatusUnauthorized {
				t.Errorf("带伪造令牌 → %d，期望 401", got)
			}
		})
	}
}

// ── 司机登录：第 8 个免登录端点 ──────────────────────────────
//
// 上面那份清单里写着 /driver/login 之外的司机端点"实测全部 401，把关是好的"，
// 但 login 本身是公开的，而且它是**凭据校验**端点——校验的是身份证后 6 位。
// 下面两条钉的是它的闸挂在哪儿。

// mkDriver 造一个能登录的司机，返回手机号与身份证后 6 位。
func (e *testEnv) mkDriver() (phone, idTail string) {
	e.t.Helper()
	phone = "139" + fmt.Sprintf("%08d", uuid.New().ID()%100000000)
	idNo := "310101199001" + fmt.Sprintf("%06d", uuid.New().ID()%1000000)
	idTail = idNo[len(idNo)-6:]
	id := uuid.NewString()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO md_driver (id, created_at, updated_at, is_deleted, name, phone, id_no,
		  license_no, is_active, license_type, qualification_cert_no, employment_type,
		  app_registered, cumulative_freight, cumulative_waybills, wechat)
		VALUES ($1::uuid, now(), now(), false, '测试司机', $2, $3,
		  '', true, '', '', 'fulltime', false, 0, 0, '')`, id, phone, idNo); err != nil {
		e.t.Fatalf("造司机失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM md_driver WHERE id=$1::uuid`, id)
	})
	return phone, idTail
}

// driverLogin 从指定来源 IP 发起一次司机登录。
// ClientIP 取 X-Forwarded-For 首段，所以换这个头等于换来源 IP——
// 攻击者手上有代理池时就是这么绕按 IP 的闸的。
func (e *testEnv) driverLogin(ip, phone, idTail string) int {
	e.t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/driver/login",
		strings.NewReader(fmt.Sprintf(`{"phone":%q,"id_tail":%q}`, phone, idTail)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec.Code
}

// TestDriverLoginThrottleSurvivesIPRotation 换 IP 不能重置爆破额度。
//
// 闸只按来源 IP 的话，它拦的是"猜的人"，而猜的人可以换。真正被反复试的
// 是**那个手机号对应的身份证后 6 位**，闸得挂在它身上——和公开查单同一个道理。
func TestDriverLoginThrottleSurvivesIPRotation(t *testing.T) {
	e := newTestEnv(t)
	phone, _ := e.mkDriver()
	throttledAt := -1
	for i := 0; i < 40; i++ {
		// 每次都换一个来源 IP，模拟代理池
		if e.driverLogin(fmt.Sprintf("203.0.113.%d", i%250), phone, fmt.Sprintf("%06d", i)) == http.StatusTooManyRequests {
			throttledAt = i
			break
		}
	}
	if throttledAt < 0 {
		t.Error("同一个手机号连试 40 次都没被限流（每次换来源 IP）—— " +
			"身份证后 6 位可以被慢慢穷举，闸挂在了 IP 上而不是被猜的那个号上")
	}
}

// TestDriverLoginSuccessDoesNotConsumeIPBudget 共用出口的司机不能互相挤掉。
//
// 司机端是手机 App，跑在运营商网络上——一个基站后面的车队共用一个出口 IP
// 是常态。按 IP 无差别计数的话，一支 20 人的车队早上集中上线就会互相挤掉。
func TestDriverLoginSuccessDoesNotConsumeIPBudget(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 25; i++ {
		phone, tail := e.mkDriver()
		if got := e.driverLogin("198.51.100.7", phone, tail); got != http.StatusOK {
			t.Fatalf("第 %d 个司机从同一出口 IP 登录变成了 %d —— "+
				"登录成功也在消耗 IP 配额，同一车队的人会互相挤掉", i+1, got)
		}
	}
}
