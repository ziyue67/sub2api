package service

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type openaiOAuthClientAuthURLStub struct{}

func (s *openaiOAuthClientAuthURLStub) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientAuthURLStub) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientAuthURLStub) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func TestOpenAIOAuthService_GenerateAuthURL_OpenAIKeepsCodexFlow(t *testing.T) {
	svc := NewOpenAIOAuthService(nil, &openaiOAuthClientAuthURLStub{})
	defer svc.Stop()

	result, err := svc.GenerateAuthURL(context.Background(), nil, "", PlatformOpenAI)
	require.NoError(t, err)
	require.NotEmpty(t, result.AuthURL)
	require.NotEmpty(t, result.SessionID)

	parsed, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	q := parsed.Query()
	require.Equal(t, openai.ClientID, q.Get("client_id"))
	require.Equal(t, "true", q.Get("codex_cli_simplified_flow"))

	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.Equal(t, openai.ClientID, session.ClientID)
}

func TestOpenAIOAuthService_GenerateAuthURL_ExcelBindsClientAndCallback(t *testing.T) {
	svc := NewOpenAIOAuthService(nil, &openaiOAuthClientAuthURLStub{})
	defer svc.Stop()
	result, err := svc.GenerateAuthURL(context.Background(), nil, "", PlatformOpenAI, "excel")
	require.NoError(t, err)
	parsed, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	require.Equal(t, "auth.openai.com", parsed.Host)
	require.Equal(t, "/api/accounts/authorize", parsed.Path)
	q := parsed.Query()
	require.Equal(t, openai.ExcelClientID, q.Get("client_id"))
	require.Equal(t, openai.ExcelRedirectURI, q.Get("redirect_uri"))
	require.Equal(t, "https://api.openai.com/v1", q.Get("audience"))
	require.Equal(t, "PC", q.Get("platform"))
	require.Equal(t, "openid offline_access email profile organization.read", q.Get("scope"))
	require.Empty(t, q.Get("codex_cli_simplified_flow"))
	require.Empty(t, q.Get("id_token_add_organizations"))
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.Equal(t, openai.ExcelClientID, session.ClientID)
	require.Equal(t, openai.ExcelRedirectURI, session.RedirectURI)
	require.Equal(t, session.State, q.Get("state"))
	require.Equal(t, openai.GenerateCodeChallenge(session.CodeVerifier), q.Get("code_challenge"))
	_, err = svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: result.SessionID, Code: "test-code", State: session.State, RedirectURI: "http://localhost:1455/auth/callback",
	})
	require.ErrorContains(t, err, "callback URI does not match")
}

func TestOpenAIOAuthService_GenerateAuthURL_RejectsUnknownClientAndExcelRedirect(t *testing.T) {
	svc := NewOpenAIOAuthService(nil, &openaiOAuthClientAuthURLStub{})
	defer svc.Stop()
	_, err := svc.GenerateAuthURL(context.Background(), nil, "", PlatformOpenAI, "unknown")
	require.ErrorContains(t, err, "unsupported OAuth client")
	_, err = svc.GenerateAuthURL(context.Background(), nil, "https://untrusted.invalid/callback", PlatformOpenAI, "excel")
	require.ErrorContains(t, err, "official callback")
}
