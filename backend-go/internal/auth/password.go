package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// VerifyDjangoPassword 校验 Django pbkdf2_sha256 口令哈希：
// 格式 pbkdf2_sha256$<iterations>$<salt>$<b64(hash)>，与 django.contrib.auth.hashers 对齐，
// 使 Go 网关可直接复用 accounts_user 存量口令，登录无需回落 Django。
func VerifyDjangoPassword(password, encoded string) (bool, error) {
	parts := strings.SplitN(encoded, "$", 4)
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false, fmt.Errorf("unsupported hasher: %s", parts[0])
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false, fmt.Errorf("bad iterations")
	}
	salt, expected := parts[2], parts[3]
	want, err := base64.StdEncoding.DecodeString(expected)
	if err != nil {
		return false, fmt.Errorf("bad hash b64")
	}
	got := pbkdf2.Key([]byte(password), []byte(salt), iterations, len(want), sha256.New)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
