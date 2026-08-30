package driver

// 司机端令牌与 Django signing 的兼容性。
//
// 这份用例原先只有一句：拿一个 Django 当年签出来的固定 token 去 UnsignToken，
// 断言返回原始 value。它是个**定时炸弹**——令牌里带签发时间戳，
// tokenMaxAge 是 7 天，所以这条断言只在夹具生成后的一周内成立，
// 之后永远失败。实测就是这么炸的：夹具签于 7 月 19 日，8 月 29 日跑时
// 报 `unsign django token: "" expired`。
//
// 问题不在夹具旧，在于**一条断言同时压了两件事**：签名格式兼容（不随时间变）
// 与令牌新鲜度（必然随时间变）。拆开就都可测了：
//   · 固定夹具只用来验「Django 的 HMAC 我们算得出来」——
//     签名校验在时效校验之前，所以过期错误本身就证明签名对上了；
//   · 完整往返用现签的令牌，永远新鲜；
//   · 篡改用例顺带钉住"先验签名再验时效"这个顺序。

import (
	"errors"
	"strings"
	"testing"
)

// 由 Django 的 signing.dumps 在并跑期签出，密钥即下方 secret。
// **不要"更新"这个夹具**：它的价值就在于不是我们自己签的。
// 它过期是预期之中的，下面的断言也正是按"过期"写的。
const (
	compatSecret = "dev-insecure-secret-change-me-min-32-bytes"
	djangoToken  = "019f7cbd-b6d3-737e-8fcc-88491c7ae226:1wp1nd:gVWsf_mtRwl3jdIZae-EieaGV0dfF4r3wdrimcrArQE"
)

// Django 签的令牌，我们必须能算出同一个签名。
// 签名校验通过（才会走到时效判断）的证据，就是错误是「过期」而不是「非法」。
func TestDjangoSignatureIsRecognised(t *testing.T) {
	_, err := UnsignToken(compatSecret, djangoToken)
	if !errors.Is(err, errTokenExpired) {
		t.Fatalf("期望 %v（说明 Django 的 HMAC 与我们算的一致，只是这张券过期了），实际 %v\n"+
			"若是 invalid，说明签名算法对不上——那才是真的兼容性破了", errTokenExpired, err)
	}
}

// 夹具的 payload 结构也要对得上 Django：value:base62时间戳:签名
func TestDjangoTokenLayout(t *testing.T) {
	parts := strings.Split(djangoToken, ":")
	if len(parts) != 3 {
		t.Fatalf("Django 令牌应为 value:ts:sig 三段，实际 %d 段", len(parts))
	}
	if parts[0] != "019f7cbd-b6d3-737e-8fcc-88491c7ae226" {
		t.Errorf("第一段应是原始 value，实际 %q", parts[0])
	}
	if _, ok := b62Decode(parts[1]); !ok {
		t.Errorf("第二段应是 base62 时间戳，解不出来：%q", parts[1])
	}
}

// 完整往返用现签的令牌——不带任何会过期的夹具
func TestSignUnsignRoundTrip(t *testing.T) {
	tok := SignToken(compatSecret, "abc")
	got, err := UnsignToken(compatSecret, tok)
	if err != nil || got != "abc" {
		t.Fatalf("现签令牌往返失败：%q %v", got, err)
	}
}

// 篡改必须报「非法」而不是「过期」：签名校验要发生在时效校验之前，
// 否则一张伪造的过期令牌会得到"过期"这种误导性的错误。
func TestTamperedTokenRejectedAsInvalid(t *testing.T) {
	for _, bad := range []string{
		djangoToken + "x", // 签名尾部加字符
		SignToken(compatSecret, "a") + "x",
		"nocolons",
		"only:two",
	} {
		if _, err := UnsignToken(compatSecret, bad); !errors.Is(err, errTokenInvalid) {
			t.Errorf("篡改/畸形令牌 %q 应报 invalid，实际 %v", truncTok(bad), err)
		}
	}
}

// 换密钥必须验不过
func TestWrongSecretRejected(t *testing.T) {
	tok := SignToken(compatSecret, "abc")
	if _, err := UnsignToken("another-secret-entirely-32-bytes-long", tok); !errors.Is(err, errTokenInvalid) {
		t.Errorf("换密钥应报 invalid，实际 %v", err)
	}
}

func truncTok(s string) string {
	if len(s) <= 24 {
		return s
	}
	return s[:24] + "…"
}
