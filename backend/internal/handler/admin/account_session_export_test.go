package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type sessionExportAdminStub struct{ service.AdminService }

func (s *sessionExportAdminStub) GetAccount(context.Context, int64) (*service.Account, error) {
	return &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"chatgpt_user_id": "user-test"}}, nil
}
func TestExportOpenAISessionValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountHandler{adminService: &sessionExportAdminStub{}}
	r := gin.New()
	r.POST("/accounts/:id/export-session", h.ExportOpenAISession)
	for _, tc := range []struct {
		id, body string
		status   int
		message  string
	}{
		{"bad", `{}`, 400, "Invalid account ID"},
		{"0", `{}`, 400, "Invalid account ID"},
		{"1", `broken`, 400, "Invalid Session export request"},
		{"1", `{}`, 400, "Cookie is required"},
		{"1", `{"cookie":"` + strings.Repeat("x", 33<<10) + `"}`, 400, "Invalid Session export request"},
	} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/accounts/"+tc.id+"/export-session", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, request)
		require.Equal(t, tc.status, w.Code)
		require.Contains(t, w.Body.String(), tc.message)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

type sessionExportHandlerTransport func(*http.Request) (*http.Response, error)

func (f sessionExportHandlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
func TestExportOpenAISessionReturnsPayloadWithoutEnvelopeInData(t *testing.T) {
	body := `{"user":{"id":"user-test"},"accessToken":"offline-web-token","expires":"2099-01-01T00:00:00Z","upstreamField":true}`
	oauth := service.NewOpenAIOAuthService(nil, nil)
	oauth.SetPrivacyClientFactory(func(string) (*req.Client, error) {
		client := req.C()
		client.GetClient().Transport = sessionExportHandlerTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		return client, nil
	})
	h := &AccountHandler{adminService: &sessionExportAdminStub{}, openaiOAuthService: oauth}
	r := gin.New()
	r.POST("/accounts/:id/export-session", h.ExportOpenAISession)
	w := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/accounts/1/export-session", strings.NewReader(`{"cookie":"__Secure-next-auth.session-token=offline-cookie"}`))
	request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, request)
	require.Equal(t, 200, w.Code)
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.JSONEq(t, body, string(envelope.Data))
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NotContains(t, w.Body.String(), "offline-cookie")
}
