package openai

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const SIWCDynamicClientID = "dynamic_agent_client"
const SIWCRedirectURI = "http://127.0.0.1:1455/auth/callback"
const SIWCTokenURL = "https://auth.openai.com/api/accounts/oauth/token"
const SIWCResource = "https://api.openai.com/v1"
const SIWCDirectScope = "chatgpt.tokens.use.direct"
const SIWCScopes = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"

func BuildSIWCAuthorizationURL(state, challenge, nonce, hostID, clientID, hint, email string) string {
	if clientID == "" {
		clientID = SIWCDynamicClientID
	}
	q := url.Values{"client_id": {clientID}, "response_type": {"code"}, "redirect_uri": {SIWCRedirectURI},
		"resource": {SIWCResource}, "scope": {SIWCScopes}, "state": {state}, "nonce": {nonce},
		"ext_agent_host_id": {hostID}, "code_challenge_method": {"S256"}, "code_challenge": {challenge}}
	if clientID == SIWCDynamicClientID {
		q.Set("agent_name_hint", "Sub2API")
	} else if hint != "" {
		q.Set("id_token_hint", hint)
	}
	if email != "" {
		q.Set("login_hint", email)
	}
	return "https://auth.openai.com/api/accounts/authorize?" + q.Encode()
}

type SIWCIdentity struct {
	jwt.RegisteredClaims
	Nonce string `json:"nonce"`
	Email string `json:"email"`
}

// VerifySIWCIdentity accepts only keys fetched from OpenAI's fixed JWKS endpoint.
func VerifySIWCIdentity(raw, clientID, nonce string, jwks []byte) (*SIWCIdentity, error) {
	var keys struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(jwks, &keys) != nil {
		return nil, errors.New("invalid OpenAI signing keys")
	}
	claims := new(SIWCIdentity)
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		for _, k := range keys.Keys {
			if kid != "" && k.Kid == kid && k.Kty == "RSA" {
				n, en := base64.RawURLEncoding.DecodeString(k.N)
				e, ee := base64.RawURLEncoding.DecodeString(k.E)
				if en != nil || ee != nil || len(n) < 256 || len(e) == 0 || len(e) > 4 {
					break
				}
				exponent := new(big.Int).SetBytes(e).Int64()
				if exponent < 3 || exponent > 2147483647 {
					break
				}
				return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent)}, nil
			}
		}
		return nil, errors.New("OpenAI signing key unavailable")
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("https://auth.openai.com"), jwt.WithAudience(clientID), jwt.WithExpirationRequired())
	if err != nil || claims.Subject == "" || nonce != "" && claims.Nonce != nonce {
		return nil, errors.New("SIWC identity validation failed")
	}
	return claims, nil
}

func HasSIWCPermission(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == SIWCDirectScope {
			return true
		}
	}
	return false
}
