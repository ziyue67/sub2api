package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOAuthReauthWorkerRouteRejectsUnsafeTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		configured  string
		provided    string
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "missing configuration",
			provided:    strings.Repeat("a", 32),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "worker is not configured",
		},
		{
			name:        "short configuration",
			configured:  strings.Repeat("a", 31),
			provided:    strings.Repeat("a", 31),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "worker is not configured",
		},
		{
			name:        "wrong token",
			configured:  strings.Repeat("a", 32),
			provided:    strings.Repeat("b", 32),
			wantStatus:  http.StatusUnauthorized,
			wantMessage: "invalid OpenAI re-auth worker token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(openAIOAuthReauthWorkerTokenEnv, tt.configured)
			router := gin.New()
			router.POST("/claim", (&OpenAIOAuthReauthHandler{}).Claim)

			req := httptest.NewRequest(http.MethodPost, "/claim", strings.NewReader(`{"worker_id":"worker-a"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-OpenAI-Reauth-Worker-Token", tt.provided)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)

			require.Equal(t, tt.wantStatus, recorder.Code)
			require.Contains(t, recorder.Body.String(), tt.wantMessage)
		})
	}
}

func TestOpenAIOAuthReauthRuntimeSettingsRequireWorkerAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	token := strings.Repeat("w", 32)
	t.Setenv(openAIOAuthReauthWorkerTokenEnv, token)
	router := gin.New()
	router.POST("/runtime-settings", (&OpenAIOAuthReauthHandler{}).RuntimeSettings)
	for _, valid := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/runtime-settings", strings.NewReader("{}"))
		if valid {
			req.Header.Set("X-OpenAI-Reauth-Worker-Token", token)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if valid {
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"worker_concurrency":null`)
		} else {
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		}
		require.NotContains(t, recorder.Body.String(), token)
	}
}

func TestAccountTokenGuardV2CredentialUpdatePreservesOmittedSwitches(t *testing.T) {
	input := (accountTokenGuardV2SaveRequest{}).input()
	require.True(t, input.PreserveEnabled)
	require.True(t, input.PreserveAutoRelogin)
	require.True(t, input.Enabled)
	require.True(t, input.AutoReloginEnabled)
	disabled := false
	input = (accountTokenGuardV2SaveRequest{Enabled: &disabled}).input()
	require.False(t, input.PreserveEnabled)
	require.False(t, input.Enabled)
	require.True(t, input.PreserveAutoRelogin)
}
