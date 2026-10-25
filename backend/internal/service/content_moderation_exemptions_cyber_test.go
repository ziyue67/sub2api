package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type exemptionCyberUpstream struct{ calls int }

func (u *exemptionCyberUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"fixture\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"fixture safety stop\"}}}\n\n"))}, nil
}
func (u *exemptionCyberUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestModerationExemptionsNeverAuthorizeCyberRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for mask := 0; mask < 4; mask++ {
		moderator, _, calls := exemptionService(t, ContentModerationModePreBlock, ContentModerationKeywordModeKeywordAndAPI, exemptionFixture(t, true, true, mask&1 != 0, mask&2 != 0))
		decision, err := moderator.Check(context.Background(), exemptionInput(true))
		require.NoError(t, err)
		require.True(t, decision.Allowed)
		require.Zero(t, *calls)
		// Admission from the two review exemptions does not change a real model
		// adapter's handling of a later explicit upstream safety termination.
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"ordinary fixture"}],"stream":true}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		upstream := &exemptionCyberUpstream{}
		gateway := &OpenAIGatewayService{httpUpstream: upstream}
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "offline-fixture", "chatgpt_account_id": "fixture"}}
		_, err = gateway.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.5")
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
		require.NotNil(t, GetOpsCyberPolicy(c))
		require.Contains(t, rec.Body.String(), "cyber_policy")
		require.Equal(t, 1, upstream.calls, "explicit safety termination must not be retried")
	}
}
