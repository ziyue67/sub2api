//go:build unit

package repository

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/golang-jwt/jwt/v5"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

func TestSIWCTokenScopeAndVerifiedIdentityOffline(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks, err := json.Marshal(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "offline", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	require.NoError(t, err)
	sign := func(exp time.Time, aud, issuer, nonce string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, openai.SIWCIdentity{RegisteredClaims: jwt.RegisteredClaims{Subject: "subscriber", Issuer: issuer, Audience: jwt.ClaimStrings{aud}, ExpiresAt: jwt.NewNumericDate(exp)}, Nonce: nonce, Email: "user@example.test"})
		token.Header["kid"] = "offline"
		raw, e := token.SignedString(key)
		require.NoError(t, e)
		return raw
	}
	valid := sign(time.Now().Add(time.Hour), "issued", "https://auth.openai.com", "nonce")
	for _, tc := range []struct {
		name, grant, scope, id, tokenType string
		expiry                            int64
		valid                             bool
	}{
		{"code_permission", "authorization_code", openai.SIWCScopes, valid, "Bearer", 3600, true},
		{"code_missing_scope", "authorization_code", "", valid, "Bearer", 3600, false},
		{"code_missing_id", "authorization_code", openai.SIWCDirectScope, "", "Bearer", 3600, false},
		{"code_wrong_nonce", "authorization_code", openai.SIWCDirectScope, sign(time.Now().Add(time.Hour), "issued", "https://auth.openai.com", "wrong"), "Bearer", 3600, false},
		{"code_wrong_audience", "authorization_code", openai.SIWCDirectScope, sign(time.Now().Add(time.Hour), "other", "https://auth.openai.com", "nonce"), "Bearer", 3600, false},
		{"code_wrong_issuer", "authorization_code", openai.SIWCDirectScope, sign(time.Now().Add(time.Hour), "issued", "https://invalid.example", "nonce"), "Bearer", 3600, false},
		{"code_expired_identity", "authorization_code", openai.SIWCDirectScope, sign(time.Now().Add(-time.Minute), "issued", "https://auth.openai.com", "nonce"), "Bearer", 3600, false},
		{"refresh_omitted_scope_and_id", "refresh_token", "", "", "Bearer", 3600, true},
		{"refresh_direct_scope", "refresh_token", openai.SIWCDirectScope, "", "Bearer", 3600, true},
		{"refresh_explicit_missing_permission", "refresh_token", "openid email", "", "Bearer", 3600, false},
		{"refresh_whitespace_scope", "refresh_token", " ", "", "Bearer", 3600, false},
		{"refresh_scope_substring", "refresh_token", openai.SIWCDirectScope + ".invalid", "", "Bearer", 3600, false},
		{"refresh_bad_identity", "refresh_token", openai.SIWCDirectScope, "invalid", "Bearer", 3600, false},
		{"refresh_zero_expiry", "refresh_token", "", "", "Bearer", 0, false},
		{"refresh_wrong_token_type", "refresh_token", "", "", "DPoP", 3600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := "http://siwc-offline.invalid:12345"
			client := req.C()
			calls := []string{}
			client.GetTransport().WrapRoundTripFunc(func(_ http.RoundTripper) req.HttpRoundTripFunc {
				return func(r *http.Request) (*http.Response, error) {
					calls = append(calls, r.URL.String())
					payload := string(jwks)
					switch r.URL.String() {
					case "https://auth.openai.com/.well-known/jwks.json":
					case openai.SIWCTokenURL:
						require.NoError(t, r.ParseForm())
						require.Equal(t, openai.SIWCResource, r.Form.Get("resource"))
						require.Equal(t, "issued", r.Form.Get("client_id"))
						require.Empty(t, r.Form.Get("scope"))
						b, e := json.Marshal(openai.TokenResponse{AccessToken: "opaque-access", RefreshToken: "rotated-refresh", IDToken: tc.id, TokenType: tc.tokenType, ExpiresIn: tc.expiry, Scope: tc.scope})
						require.NoError(t, e)
						payload = string(b)
					default:
						t.Fatalf("unexpected external request: %s", r.URL)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
				}
			})
			poolKey := buildReqClientKey(reqClientOptions{ProxyURL: proxy, Timeout: 120 * time.Second})
			sharedReqClients.Store(poolKey, client)
			defer sharedReqClients.Delete(poolKey)
			nonce := ""
			if tc.grant == "authorization_code" {
				nonce = "nonce"
			}
			token, identity, err := (&openaiOAuthService{}).SIWCToken(context.Background(), url.Values{"grant_type": {tc.grant}, "client_id": {"issued"}}, proxy, nonce)
			require.Equal(t, []string{"https://auth.openai.com/.well-known/jwks.json", openai.SIWCTokenURL}, calls)
			if !tc.valid {
				require.Error(t, err)
				require.Nil(t, token)
				require.Nil(t, identity)
				require.NotContains(t, err.Error(), "opaque-access")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "opaque-access", token.AccessToken)
			if tc.id == "" {
				require.Nil(t, identity)
			} else {
				require.Equal(t, "subscriber", identity.Subject)
			}
		})
	}
}
