// Package driver 司机端 H5 免登录门户：手机号 + 身份证后 6 位登录，
// 任务列表 / 提醒强制确认 / 打卡签到（自动定位 + 水印照片）/ 证件自助上传。
package driver

// 司机端 token 与 django.core.signing.TimestampSigner(salt="driver-portal") 完全兼容：
// 形如 <value>:<b62(unix)>:<b64url(hmac)>，密钥同为 DJANGO_SECRET_KEY。
// 兼容是硬要求 —— 切换到 Go 时司机端已发出的 7 天有效 token 不能一夜作废，
// 否则所有在途司机会被同时踢下线。

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

const (
	signerSalt  = "driver-portal"
	tokenMaxAge = 7 * 24 * time.Hour
)

var (
	errTokenExpired = errors.New("expired")
	errTokenInvalid = errors.New("invalid")
)

// b62 对齐 django.utils.http.base36/base62 的字符表（0-9A-Za-z）
const b62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func b62Encode(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var out []byte
	for n > 0 {
		out = append([]byte{b62Alphabet[n%62]}, out...)
		n /= 62
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func b62Decode(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var n int64
	for _, c := range []byte(s) {
		i := strings.IndexByte(b62Alphabet, c)
		if i < 0 {
			return 0, false
		}
		n = n*62 + int64(i)
	}
	if neg {
		n = -n
	}
	return n, true
}

// signature 复刻 django.utils.crypto.salted_hmac + base64_hmac：
// key = sha256(key_salt + secret)，再用该 key 对 value 做 HMAC-SHA256，
// 输出 urlsafe base64 去掉补位等号。
func signature(secret, value string) string {
	h := sha256.Sum256([]byte(signerSalt + "signer" + secret))
	mac := hmac.New(sha256.New, h[:])
	mac.Write([]byte(value))
	return strings.TrimRight(base64.URLEncoding.EncodeToString(mac.Sum(nil)), "=")
}

// SignToken 签发司机端 token（value 为司机主键）
func SignToken(secret, value string) string {
	payload := value + ":" + b62Encode(time.Now().Unix())
	return payload + ":" + signature(secret, payload)
}

// UnsignToken 校验并返回 value；区分「已过期」与「非法」，两者的前端处置不同
func UnsignToken(secret, token string) (string, error) {
	i := strings.LastIndex(token, ":")
	if i < 0 {
		return "", errTokenInvalid
	}
	payload, sig := token[:i], token[i+1:]
	if subtle.ConstantTimeCompare([]byte(signature(secret, payload)), []byte(sig)) != 1 {
		return "", errTokenInvalid
	}
	j := strings.LastIndex(payload, ":")
	if j < 0 {
		return "", errTokenInvalid
	}
	value, tsRaw := payload[:j], payload[j+1:]
	ts, ok := b62Decode(tsRaw)
	if !ok {
		return "", errTokenInvalid
	}
	if time.Since(time.Unix(ts, 0)) > tokenMaxAge {
		return "", errTokenExpired
	}
	return value, nil
}
