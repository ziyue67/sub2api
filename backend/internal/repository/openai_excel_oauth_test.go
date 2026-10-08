package repository

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestExcelOAuthUsesOfficialTokenContract(t *testing.T) {
	for _, grant := range []string{"refresh_token", "authorization_code"} {
		t.Run(grant, func(t *testing.T) {
			var got *http.Request
			server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				got = r
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"new-at","refresh_token":"new-rt","expires_in":3600}`))
			}))
			defer server.Close()
			client := &openaiOAuthService{tokenURL: server.URL + "/oauth/token?existing=kept"}
			var result *openai.TokenResponse
			var err error
			if grant == "refresh_token" {
				result, err = client.RefreshTokenWithClientID(context.Background(), "excel-rt", "", openai.ExcelClientID)
			} else {
				result, err = client.ExchangeCode(context.Background(), "code", "verifier", "https://auth.openai.com/basispoints/deviceauth/callback", "", openai.ExcelClientID)
			}
			require.NoError(t, err)
			require.Equal(t, "new-at", result.AccessToken)
			require.NotNil(t, got)
			require.Equal(t, "true", got.URL.Query().Get("unified"))
			require.Equal(t, "kept", got.URL.Query().Get("existing"))
			require.Equal(t, openai.ExcelClientID, got.PostForm.Get("client_id"))
			require.Equal(t, grant, got.PostForm.Get("grant_type"))
			require.False(t, got.PostForm.Has("scope"))
			require.Empty(t, got.Header.Get("originator"))
		})
	}
}

func TestExcelOAuthRejectsWithoutEchoingCredentialBody(t *testing.T) {
	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"private-credential"}`))
	}))
	defer server.Close()
	client := &openaiOAuthService{tokenURL: server.URL}
	_, err := client.RefreshTokenWithClientID(context.Background(), "excel-rt", "", openai.ExcelClientID)
	require.ErrorContains(t, err, "status 401")
	require.NotContains(t, err.Error(), "private-credential")
	_, err = client.ExchangeCode(context.Background(), "code", "verifier", openai.ExcelRedirectURI, "", openai.ExcelClientID)
	require.ErrorContains(t, err, "status 401")
	require.NotContains(t, err.Error(), "private-credential")
}
