package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// simplejwt 兼容层：同 HS256、同密钥（DJANGO_SECRET_KEY）、同 claims
// {token_type, exp, iat, jti, user_id}。并跑期内 Go 与 Django 互认对方签发的 token，
// 前端与已登录用户零感知切换。
type Claims struct {
	TokenType string `json:"token_type"`
	UserID    string `json:"user_id"`
	JTI       string `json:"jti"`
	jwt.RegisteredClaims
}

type TokenIssuer struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewIssuer(secret string, accessMinutes, refreshDays int) *TokenIssuer {
	return &TokenIssuer{
		secret:     []byte(secret),
		accessTTL:  time.Duration(accessMinutes) * time.Minute,
		refreshTTL: time.Duration(refreshDays) * 24 * time.Hour,
	}
}

func randomJTI() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (t *TokenIssuer) sign(tokenType, userID string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		TokenType: tokenType,
		UserID:    userID,
		JTI:       randomJTI(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

func (t *TokenIssuer) IssuePair(userID string) (access, refresh string, err error) {
	if access, err = t.sign("access", userID, t.accessTTL); err != nil {
		return
	}
	refresh, err = t.sign("refresh", userID, t.refreshTTL)
	return
}

// Parse 校验签名与过期，并要求 token_type 匹配（access/refresh 不能混用）。
func (t *TokenIssuer) Parse(raw, wantType string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return t.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	if claims.TokenType != wantType {
		return nil, fmt.Errorf("token type mismatch: got %s want %s", claims.TokenType, wantType)
	}
	if claims.UserID == "" {
		return nil, fmt.Errorf("missing user_id")
	}
	return claims, nil
}
