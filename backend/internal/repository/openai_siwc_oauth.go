package repository

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// Tokens and signing keys use the same selected account proxy. No credentials
// or token endpoint response bodies are included in errors.
func (s *openaiOAuthService) SIWCToken(ctx context.Context, form url.Values, proxyURL, nonce string) (*openai.TokenResponse, *openai.SIWCIdentity, error) {
	client, err := createOpenAIReqClient(proxyURL)
	if err != nil {
		return nil, nil, errors.New("SIWC proxy is unavailable")
	}
	// Fetch signing material before consuming a rotating refresh token.
	keys, errKeys := client.R().SetContext(ctx).Get("https://auth.openai.com/.well-known/jwks.json")
	if errKeys != nil || keys == nil || !keys.IsSuccessState() {
		return nil, nil, errors.New("OpenAI signing keys unavailable")
	}
	form.Set("resource", openai.SIWCResource)
	var token openai.TokenResponse
	resp, err := client.R().SetContext(ctx).SetFormDataFromValues(form).SetSuccessResult(&token).Post(openai.SIWCTokenURL)
	if err != nil || resp == nil || !resp.IsSuccessState() {
		return nil, nil, errors.New("SIWC token exchange or refresh failed; credentials retained")
	}
	if token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer") {
		return nil, nil, errors.New("SIWC token type is unsupported")
	}
	if token.AccessToken == "" || token.ExpiresIn <= 0 || !siwcScopeAllowsDirectUse(token.Scope, form.Get("grant_type")) {
		return nil, nil, errors.New("SIWC grant does not permit ChatGPT plan usage")
	}
	var identity *openai.SIWCIdentity
	if token.IDToken != "" {
		identity, err = openai.VerifySIWCIdentity(token.IDToken, form.Get("client_id"), nonce, keys.Bytes())
		if err != nil {
			return nil, nil, err
		}
	} else if form.Get("grant_type") == "authorization_code" {
		return nil, nil, errors.New("SIWC identity token is missing")
	}
	return &token, identity, nil
}

// Authorization must explicitly grant plan usage. Refresh may omit scope to
// retain the previous grant, but any returned scope must still allow direct use.
func siwcScopeAllowsDirectUse(scope, grantType string) bool {
	if scope == "" {
		return grantType == "refresh_token"
	}
	return openai.HasSIWCPermission(scope)
}
