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
