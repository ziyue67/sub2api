//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the HTTP failover entry, including persistent account policy, rather
// than just matching a capacity string. This used to persist a 30 minute model
// ban before the pool's same-account retry could run.
func TestPoolCapacityHTTPDoesNotCascadeIntoModelCooldown(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{rateLimitService: rateLimits}
			account := newTempUnschedScopeAccount("model", map[string]any{
				"error_code": float64(status), "keywords": []any{"capacity"}, "duration_minutes": float64(30),
			})
			account.Credentials["pool_mode"] = true
			body := []byte(`{"error":{"type":"invalid_request_error","message":"Selected model is at capacity. Please try a different model."}}`)
			for attempt := 0; attempt < 3; attempt++ {
				failure := svc.failoverOpenAIUpstreamHTTPError(context.Background(), nil, account,
					&http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"2"}}}, body,
					"Selected model is at capacity. Please try a different model.", "gpt-6.1-sol")
				require.NotNil(t, failure)
				require.True(t, failure.RequestScopedTransient)
				require.True(t, failure.RetryableOnSameAccount)
				require.Equal(t, http.StatusServiceUnavailable, failure.ClientStatusCode)
				require.Equal(t, "2", failure.ResponseHeaders.Get("Retry-After"))
			}
			require.Empty(t, repo.modelRateLimitCalls)
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.setErrCalls)
			require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6.1-sol", false))
		})
	}
}

func TestPoolCapacityHTTPDetailEnvelopeKeepsExistingOverloadSemantics(t *testing.T) {
	repo := &errorPolicyRepoStub{}
	svc := &OpenAIGatewayService{rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
	account := newTempUnschedScopeAccount("model", map[string]any{"keywords": []any{"overloaded"}})
	account.Credentials["pool_mode"] = true
	body := []byte(`{"detail":"Our servers are currently overloaded. Please try again later."}`)
	failure := svc.failoverOpenAIUpstreamHTTPError(context.Background(), nil, account,
		&http.Response{StatusCode: 503, Header: http.Header{}}, body,
		"Our servers are currently overloaded. Please try again later.", "gpt-6.1-sol")
	require.NotNil(t, failure)
	require.True(t, failure.RequestScopedTransient)
	require.Empty(t, repo.modelRateLimitCalls)
}

func TestPoolCapacityExplicitQuotaOutranksCapacityWording(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		headers http.Header
	}{
		{"quota-code", `{"error":{"type":"usage_limit_reached","code":"insufficient_quota","message":"Selected model is at capacity"}}`, nil},
		{"quota-reset", `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","resets_at":2000000000,"message":"Selected model is at capacity"}}`, nil},
		{"exhausted-window", `{"error":{"type":"rate_limit_error","message":"Selected model is at capacity"}}`, http.Header{
			"X-Codex-Primary-Used-Percent": {"100"}, "X-Codex-Primary-Reset-After-Seconds": {"3600"}, "X-Codex-Primary-Window-Minutes": {"300"},
		}},
		{"persisted-relay-quota", `{"error":{"type":"api_error","code":"model_capacity_exhausted","message":"Selected model is at capacity"}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &errorPolicyRepoStub{}
			svc := &OpenAIGatewayService{rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
			account := newTempUnschedScopeAccount("model", map[string]any{
				"error_code": float64(429), "keywords": []any{"capacity"}, "duration_minutes": float64(30),
			})
			account.Credentials["pool_mode"] = true
			failure := svc.failoverOpenAIUpstreamHTTPError(context.Background(), nil, account,
				&http.Response{StatusCode: 429, Header: tc.headers}, []byte(tc.body), "Selected model is at capacity", "gpt-6.1-sol")
			require.NotNil(t, failure)
			require.False(t, failure.RequestScopedTransient)
			require.False(t, failure.RetryableOnSameAccount, "explicit quota rule must stop retrying this account")
			require.Len(t, repo.modelRateLimitCalls, 1)
			require.Equal(t, "gpt-6.1-sol", repo.modelRateLimitCalls[0].scope)
		})
	}
}

func TestPoolCapacitySSEClassificationAndSafetyBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		payload  string
		capacity bool
		failover bool
	}{
		{"capacity-code-with-generic-message", `{"type":"response.failed","response":{"error":{"type":"server_error","code":"upstream_capacity_exhausted","message":"Temporarily unavailable"}}}`, true, true},
		{"capacity-error", `{"type":"error","error":{"type":"rate_limit_error","message":"Selected model is at capacity"}}`, true, true},
		{"quota", `{"type":"response.failed","response":{"error":{"type":"usage_limit_reached","code":"insufficient_quota","message":"Selected model is at capacity"}}}`, false, true},
		{"authentication", `{"type":"response.failed","response":{"error":{"type":"authentication_error","code":"invalid_api_key","message":"Selected model is at capacity"}}}`, false, true},
		{"safety", `{"type":"response.failed","response":{"error":{"type":"content_policy_violation","code":"content_policy_violation","message":"Selected model is at capacity"}}}`, false, false},
		{"echoed-input", `{"type":"error","error":{"message":"invalid input"},"input":"Selected model is at capacity"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newNonStreamingFailoverContext(t)
			svc := newNonStreamingFailoverService()
			payload := []byte(tc.payload)
			eventType := gjson.GetBytes(payload, "type").String()
			_, err := svc.handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(),
				sseTerminalBody(eventType, tc.payload), "gpt-6.1-sol", "gpt-6.1-sol")
			require.Error(t, err)
			var failure *UpstreamFailoverError
			require.Equal(t, tc.failover, errors.As(err, &failure))
			if failure != nil {
				require.Equal(t, tc.capacity, failure.RequestScopedTransient)
				require.False(t, c.Writer.Written())
			} else {
				require.True(t, c.Writer.Written())
				require.NotEmpty(t, rec.Body.String())
			}
		})
	}
}

func TestPoolCapacityStreamingBeforeOutputRemainsSafeToSwitch(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, event := range []string{"error", "response.failed"} {
			t.Run(fmt.Sprintf("passthrough=%t/event=%s", passthrough, event), func(t *testing.T) {
				repo := &errorPolicyRepoStub{}
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
					rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
				account := newTempUnschedScopeAccount("model", map[string]any{
					"keywords": []any{"capacity"}, "duration_minutes": float64(30),
				})
				account.Credentials["pool_mode"] = true
				c, rec := newNonStreamingFailoverContext(t)
				payload := fmt.Sprintf(`{"type":%q,"error":{"type":"server_error","message":"Selected model is at capacity"}}`, event)
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Retry-After": {"2"}},
					Body: io.NopCloser(strings.NewReader("event: " + event + "\ndata: " + payload + "\n\n"))}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
				} else {
					_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
				}
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.RequestScopedTransient)
				require.Equal(t, http.StatusServiceUnavailable, failure.ClientStatusCode)
				require.Equal(t, 2*time.Second, failure.SameAccountRetryDelay)
				require.False(t, c.Writer.Written())
				require.Empty(t, rec.Body.String())
				require.Empty(t, repo.modelRateLimitCalls)
			})
		}
	}
}
