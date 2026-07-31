package config

// 启动前置检查：把「配错了也能跑起来，跑起来才发现」的那些坑改成开不了机。
//
// 现状里最危险的一条：DJANGO_SECRET_KEY 有默认值
// （dev-insecure-secret-change-me-min-32-bytes），编排文件里也写着
// CHANGE-ME-IN-PRODUCTION-USE-32BYTES+。忘了改的话服务照样正常启动、
// 正常签发 JWT——而这把密钥同时用于内部用户令牌与司机端令牌，
// 一旦是公开已知的默认值，任何人都能自己签一张管理员令牌进来。
// 这类问题不会在测试环境暴露：测试环境本来就用默认值，一切正常。
//
// 所以：**非 DEBUG 模式下，弱密钥直接拒绝启动**。
// 开发环境（DJANGO_DEBUG=true，默认）不受影响，一行配置都不用加。

import (
	"errors"
	"fmt"
	"strings"
)

// 已知的占位密钥。它们出现在代码默认值、编排文件、示例文档里，
// 也就必然出现在某些人的生产环境里。
var placeholderSecrets = []string{
	"dev-insecure-secret-change-me-min-32-bytes",
	"CHANGE-ME-IN-PRODUCTION-USE-32BYTES+",
	"ci-insecure-secret-min-32-bytes-long",
	"test-insecure-secret-min-32-bytes-long!!",
	"changeme",
	"secret",
}

// Preflight 在监听之前跑。返回错误即应拒绝启动。
//
// 只在 Debug=false 时收紧：开发要能一键跑起来，这是它比什么都重要的属性。
func (c Config) Preflight() error {
	if c.Debug {
		return nil
	}
	var problems []string

	key := strings.TrimSpace(c.SecretKey)
	switch {
	case key == "":
		problems = append(problems, "DJANGO_SECRET_KEY 为空")
	case len(key) < 32:
		problems = append(problems,
			fmt.Sprintf("DJANGO_SECRET_KEY 只有 %d 字节，HS256 签名密钥至少要 32 字节", len(key)))
	default:
		for _, p := range placeholderSecrets {
			if strings.EqualFold(key, p) {
				problems = append(problems,
					"DJANGO_SECRET_KEY 还是占位值——它同时签发内部用户令牌与司机端令牌，"+
						"用公开已知的默认值等于任何人都能自签一张管理员令牌")
				break
			}
		}
	}

	// CORS 白名单为 * 等于对所有站点开放带凭证的跨站请求
	for _, o := range c.CORSOrigins {
		if strings.TrimSpace(o) == "*" {
			problems = append(problems, "DJANGO_CORS_ORIGINS 含通配符 *，生产必须写明确的来源")
		}
	}

	// 生产还开着自助注册，是一个「任何人都能拿到已认证身份」的入口。
	// 这条只是警告级别的提醒——确实有部署需要开（对外客户门户），
	// 所以不阻断启动，但要让它在日志里显眼。
	if c.AllowSelfRegistration {
		problems = append(problems, "warn:TMS_ALLOW_SELF_REGISTRATION=1，生产开放自助注册请确认这是有意的")
	}

	var fatal []string
	for _, p := range problems {
		if !strings.HasPrefix(p, "warn:") {
			fatal = append(fatal, "  · "+p)
		}
	}
	if len(fatal) > 0 {
		return errors.New("生产配置检查未通过（DJANGO_DEBUG=false）：\n" +
			strings.Join(fatal, "\n") +
			"\n改好上述配置再启动；确需跳过请显式设 DJANGO_DEBUG=true（仅限非生产）")
	}
	return nil
}

// Warnings 返回不阻断启动、但值得在日志里显眼的问题
func (c Config) Warnings() []string {
	if c.Debug {
		return nil
	}
	var out []string
	if c.AllowSelfRegistration {
		out = append(out, "生产环境开放了自助注册（TMS_ALLOW_SELF_REGISTRATION=1），请确认这是有意的")
	}
	if c.PublicBase == "" || strings.HasPrefix(c.PublicBase, "http://127.0.0.1") {
		out = append(out, "PUBLIC_BASE_URL 仍是本机地址，头像/回单等媒体文件的绝对地址会指向 127.0.0.1")
	}
	if len(c.CORSOrigins) == 1 && strings.Contains(c.CORSOrigins[0], "localhost") {
		out = append(out, "DJANGO_CORS_ORIGINS 仍是开发默认值，前端从正式域名访问会被 CORS 挡住")
	}
	return out
}
