package repository

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// All OAuth registrations retain their actual client. Signing keys are fetched
// before spending a one-time code or rotating refresh token.
func (s *openaiOAuthService) ConnectionToken(ctx context.Context, form url.Values, proxy, nonce, kind string) (*openai.TokenResponse, *openai.SIWCIdentity, error) {
	client, err := createOpenAIReqClient(proxy)
	if err != nil {
		return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "not_sent"}
	}
	keys, err := client.R().SetContext(ctx).Get("https://auth.openai.com/.well-known/jwks.json")
	if err != nil || keys == nil || !keys.IsSuccessState() {
		return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "not_sent"}
	}
	clientID := form.Get("client_id")

	endpoint := s.tokenURLForClient(clientID)
	switch kind {
	case "siwc":
		if clientID == "" || clientID == openai.SIWCDynamicClientID {
			return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "not_sent"}
		}
		form.Set("resource", openai.SIWCResource)
		endpoint = openai.SIWCTokenURL
	case "codex", "bps":
		expected := openai.ClientID
		if kind == "bps" {
			expected = openai.ExcelClientID
		}
		if clientID != expected {
			return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "not_sent"}
		}
		if kind == "codex" && form.Get("grant_type") == "refresh_token" {
			form.Set("scope", openai.RefreshScopes)
		}
	default:
		return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "not_sent"}
	}
	if err = ctx.Err(); err != nil {
		return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "not_sent"}
	}
	token := new(openai.TokenResponse)
	request := client.R().SetContext(ctx).SetFormDataFromValues(form).SetSuccessResult(token)
	if kind == "codex" {
		ua, originator := service.CodexCanonicalAuthIdentity()
		request.SetHeader("User-Agent", ua).SetHeader("originator", originator)
	}
	response, postErr := request.Post(endpoint)
	if postErr != nil || response == nil {
		return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "uncertain"}
	}
	if !response.IsSuccessState() {
		outcome := "uncertain"
		// Only structured OAuth rejection is certain. Arbitrary 5xx/HTML does not
		// prove that an intermediary failed before the rotating token was spent.
		var rejection struct {
			Error string `json:"error"`
		}
		if (response.StatusCode == 400 || response.StatusCode == 401 || response.StatusCode == 403) && json.Unmarshal(response.Bytes(), &rejection) == nil && rejection.Error != "" {
			outcome = "rejected"
		}
		return nil, nil, &service.OpenAIConnectionTokenError{Outcome: outcome}
	}

	if token == nil || token.AccessToken == "" || token.ExpiresIn <= 0 || token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer") {
		return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "uncertain"}
	}
	var identity *openai.SIWCIdentity
	if token.IDToken != "" {
		identity, err = openai.VerifySIWCIdentity(token.IDToken, clientID, nonce, keys.Bytes())
		if err != nil {
			return nil, nil, &service.OpenAIConnectionTokenError{Outcome: "uncertain"}
		}
	} else if form.Get("grant_type") == "authorization_code" {
		return nil, nil, errors.New("connection identity token missing")
	}
	// An identity-only SIWC grant is retained for sign-in, with no inference
	// capabilities. It is never converted into Codex authorization.
	return token, identity, nil
}

func (s *openaiOAuthService) RevokeConnectionToken(ctx context.Context, token, clientID, proxy string) error {
	if token == "" || clientID == "" {
		return errors.New("renewable session is unavailable")
	}
	client, err := createOpenAIReqClient(proxy)
	if err != nil {
		return errors.New("revocation proxy unavailable")
	}
	discovery, err := client.R().SetContext(ctx).Get("https://auth.openai.com/.well-known/openid-configuration")
	if err != nil || discovery == nil || !discovery.IsSuccessState() {
		return errors.New("revocation discovery unavailable")
	}
	var config struct {
		RevocationEndpoint string `json:"revocation_endpoint"`
	}
	if json.Unmarshal(discovery.Bytes(), &config) != nil {
		return errors.New("invalid revocation discovery")
	}
	endpoint, err := url.Parse(config.RevocationEndpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host != "auth.openai.com" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("invalid revocation endpoint")
	}
	result, err := client.R().SetContext(ctx).SetFormData(map[string]string{"token": token, "token_type_hint": "refresh_token", "client_id": clientID}).Post(endpoint.String())
	if err != nil || result == nil || result.StatusCode != 200 {
		return errors.New("remote revocation was not confirmed")
	}
	return nil
}
