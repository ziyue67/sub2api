package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIInitialAdmissionMarker(t *testing.T) {
	for _, reason := range []string{"account_binding_changed", "latest_state_unavailable", "account_ineligible", "model_ticket_unavailable", "group_membership_changed"} {
		t.Run(reason, func(t *testing.T) {
			original := denyOpenAITurn(reason)
			marked := markOpenAIInitialAdmissionError(original)
			require.False(t, IsOpenAIInitialAdmissionRejection(original), "later admission errors must remain non-retryable")
			require.Equal(t, reason == "account_binding_changed", IsOpenAIInitialAdmissionRejection(fmt.Errorf("wrapped: %w", marked)))
			require.True(t, IsOpenAITurnAdmissionError(marked))
		})
	}
}

func TestOpenAIInitialAdmissionRecoveryDoesNotMarkReplaySafe(t *testing.T) {
	selected := ticketTestAccount(100)
	latest := *selected
	latest.Extra = map[string]any{"openai_excel_bps": true}
	svc := &OpenAIGatewayService{accountRepo: &turnAdmissionRepo{account: &latest}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := svc.forwardAsChatCompletions(markAgentIdentityTaskRecoveryTried(context.Background()), c, selected,
		[]byte(`{"model":"gpt-6-astra","messages":[]}`), "", "", false)
	require.True(t, IsOpenAITurnAdmissionError(err))
	require.False(t, IsOpenAIInitialAdmissionRejection(err), "task recovery has already sent an upstream request")
}
