package service

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestValidateAffiliateAttributionToken(t *testing.T) {
	svc := &AuthService{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-affiliate-secret"}}}
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, AffiliateAttributionClaims{
		InviterID: 42,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "sub2api-affiliate",
			Subject:   "click",
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}).SignedString([]byte("test-affiliate-secret"))
	require.NoError(t, err)

	inviterID, err := svc.ValidateAffiliateAttributionToken(token)
	require.NoError(t, err)
	require.Equal(t, int64(42), inviterID)

	// 篡改样本必须**确定性地**破坏签名：JWT 签名是 32 字节，base64url 编码后最后一个
	// 字符只有 4 个有效位(另 2 位被解码器忽略) —— 当它是 'w' 时改成 'x' 会解码出完全
	// 相同的签名，校验照样通过、断言偶发失败（实测 2000 次里 118 次假通过 ≈ 5.9%，
	// 在 CI 上表现为随机红）。改为翻转签名字节后重新编码。
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	sig, decodeErr := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, decodeErr)
	require.NotEmpty(t, sig)
	sig[0] ^= 0xFF
	tampered := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)
	_, err = svc.ValidateAffiliateAttributionToken(tampered)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestValidateAffiliateAttributionTokenRejectsExpired(t *testing.T) {
	svc := &AuthService{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-affiliate-secret"}}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, AffiliateAttributionClaims{
		InviterID: 42,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "sub2api-affiliate",
			Subject:   "click",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}).SignedString([]byte("test-affiliate-secret"))
	require.NoError(t, err)

	_, err = svc.ValidateAffiliateAttributionToken(token)
	require.ErrorIs(t, err, ErrInvalidToken)
}
