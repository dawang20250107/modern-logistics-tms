package config

// 启动前置检查的用例。
//
// 这组断言守的是一个很容易被"简化"掉的取舍：**开发要能零配置一键跑起来，
// 生产要在配错时开不了机**。谁把 Debug 那条短路去掉，开发环境立刻起不来；
// 谁把占位密钥名单删了，生产就能带着公开已知的密钥上线。

import "testing"

func TestPreflightSkippedInDebug(t *testing.T) {
	// 开发环境（默认 DJANGO_DEBUG=true）必须零配置可跑，
	// 哪怕密钥就是那个不安全的默认值
	c := Config{Debug: true, SecretKey: "dev-insecure-secret-change-me-min-32-bytes"}
	if err := c.Preflight(); err != nil {
		t.Errorf("DEBUG 模式不该做前置检查，却报了：%v", err)
	}
}

func TestPreflightRejectsPlaceholderSecret(t *testing.T) {
	for _, key := range placeholderSecrets {
		c := Config{Debug: false, SecretKey: key, CORSOrigins: []string{"https://x.example.com"}}
		if err := c.Preflight(); err == nil {
			t.Errorf("占位密钥 %q 应被拒绝 —— 它同时签发内部用户令牌与司机端令牌，"+
				"公开已知就等于谁都能自签管理员令牌", key)
		}
	}
}

func TestPreflightRejectsShortOrEmptySecret(t *testing.T) {
	for _, key := range []string{"", "短", "0123456789abcdef"} { // < 32 字节
		c := Config{Debug: false, SecretKey: key, CORSOrigins: []string{"https://x.example.com"}}
		if err := c.Preflight(); err == nil {
			t.Errorf("密钥 %q（%d 字节）应被拒绝：HS256 至少要 32 字节", key, len(key))
		}
	}
}

func TestPreflightRejectsWildcardCORS(t *testing.T) {
	c := Config{Debug: false, SecretKey: strongKey, CORSOrigins: []string{"*"}}
	if err := c.Preflight(); err == nil {
		t.Error("CORS 通配符应被拒绝：* + 带凭证的跨站请求 = 任意站点可代表已登录用户发请求")
	}
}

func TestPreflightPassesWithSaneProdConfig(t *testing.T) {
	c := Config{Debug: false, SecretKey: strongKey,
		CORSOrigins: []string{"https://tms.example.com"},
		PublicBase:  "https://tms.example.com"}
	if err := c.Preflight(); err != nil {
		t.Errorf("正常生产配置不该被拦：%v", err)
	}
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("正常生产配置不该有提醒，实际：%v", w)
	}
}

// 自助注册开着只警告不阻断 —— 确实有部署需要开（对外客户门户）
func TestSelfRegistrationWarnsButDoesNotBlock(t *testing.T) {
	c := Config{Debug: false, SecretKey: strongKey,
		CORSOrigins:           []string{"https://tms.example.com"},
		PublicBase:            "https://tms.example.com",
		AllowSelfRegistration: true}
	if err := c.Preflight(); err != nil {
		t.Errorf("开放注册不该阻断启动：%v", err)
	}
	if len(c.Warnings()) == 0 {
		t.Error("生产开着自助注册应给出提醒")
	}
}

const strongKey = "Kx9pQ2mV7nR4tY6wA1sD3fG5hJ8kL0zX2cB4vN6mQ8="
