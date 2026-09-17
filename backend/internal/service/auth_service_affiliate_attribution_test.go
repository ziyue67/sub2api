package service

import (
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

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	replacement := byte('A')
	if parts[2][0] == replacement {
		replacement = 'B'
	}
	parts[2] = string(replacement) + parts[2][1:]
	tampered := strings.Join(parts, ".")
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
