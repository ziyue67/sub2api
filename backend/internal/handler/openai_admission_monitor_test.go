package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdmissionModelRejectionExcludedFromSLA(t *testing.T) {
	for _, reason := range []string{"model_not_supported", "model_rate_limited", "model_ticket_unavailable", "latest_state_unavailable", "account_ineligible"} {
		t.Run(reason, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				h := &OpenAIGatewayHandler{}
				h.handleTurnAdmissionError(c, &service.OpenAITurnAdmissionError{Reason: reason}, stream)
				excluded := reason == "model_not_supported"
				require.Equal(t, excluded, service.HasOpsClientBusinessLimited(c))
				if excluded {
					require.Contains(t, w.Body.String(), "model_not_found")
					if !stream {
						require.Equal(t, 404, w.Code)
					}
					phase, limited, owner, _ := classifyOpsErrorLog(c, "model_not_found", "The requested model is not supported by the selected account", "", 404)
					require.Equal(t, "request", phase)
					require.True(t, limited)
					require.Equal(t, "client", owner)
				} else {
					require.Contains(t, w.Body.String(), "admission_unavailable")
					if !stream {
						require.Equal(t, 503, w.Code)
					}
				}
			}
		})
	}
}
