package config

import (
	"os"
	"testing"
)

// TestTokenLifetimesAreConfigurable 令牌有效期必须真的可配。
//
// .env 模板里一直写着 JWT_ACCESS_MIN / JWT_REFRESH_DAYS，而代码原先写死
// 30 分钟 / 7 天——要求更短会话的客户改了不生效，还以为配上了。
func TestTokenLifetimesAreConfigurable(t *testing.T) {
	t.Setenv("JWT_ACCESS_MIN", "5")
	t.Setenv("JWT_REFRESH_DAYS", "1")
	c := Load()
	if c.AccessMinutes != 5 {
		t.Errorf("JWT_ACCESS_MIN=5 没生效，实际 %d 分钟", c.AccessMinutes)
	}
	if c.RefreshDays != 1 {
		t.Errorf("JWT_REFRESH_DAYS=1 没生效，实际 %d 天", c.RefreshDays)
	}
	// 不设时要回到原来的默认值，不能因为可配就改了默认行为
	os.Unsetenv("JWT_ACCESS_MIN")
	os.Unsetenv("JWT_REFRESH_DAYS")
	d := Load()
	if d.AccessMinutes != 30 || d.RefreshDays != 7 {
		t.Errorf("默认值变了：%d 分钟 / %d 天（应为 30 / 7）", d.AccessMinutes, d.RefreshDays)
	}
}
