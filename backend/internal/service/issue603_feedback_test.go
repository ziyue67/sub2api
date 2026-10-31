//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestIssue603DefaultSchedulerUsesScopedFeedback(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	now := time.Now()
	clock := now
	firstLast, secondLast := now.Add(-time.Hour), now.Add(-time.Minute)
	accounts := []Account{
		{ID: 6031, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 10, LastUsedAt: &firstLast},
		{ID: 6032, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 10, LastUsedAt: &secondLast},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cfg: cfg, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}), cache: &schedulerTestGatewayCache{}, openaiAccountStats: &openAIAccountRuntimeStats{now: func() time.Time { return clock }}}
	ctx := WithPrioritySchedulingObservation(context.Background(), nil, "fixture", "/v1/responses")
	key := priorityAttemptKey{accountID: 6031, model: "fixture", endpoint: "/v1/responses"}
	for range 4 {
		svc.openaiAccountStats.attempts.report(key, false, now)
	}
	require.Zero(t, svc.defaultFeedbackPenalty(ctx, nil, "fixture", 6031), "minimum five samples")
	svc.openaiAccountStats.attempts.report(key, false, now)
	selection, _, err := svc.SelectAccountWithScheduler(ctx, nil, "", "", "fixture", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(6032), selection.Account.ID, "default path must prefer a healthy eligible peer after repeated upstream failures")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	require.Zero(t, svc.defaultFeedbackPenalty(context.Background(), nil, "other", 6031), "public model isolation")
	require.Zero(t, svc.defaultFeedbackPenalty(WithPrioritySchedulingObservation(context.Background(), nil, "fixture", "/v1/chat/completions"), nil, "fixture", 6031), "endpoint isolation")
	group := int64(9)
	require.Zero(t, svc.defaultFeedbackPenalty(ctx, &group, "fixture", 6031), "group isolation")
	require.LessOrEqual(t, svc.defaultFeedbackPenalty(ctx, nil, "fixture", 6031), 35.0)
	clock = now.Add(11 * time.Minute)
	require.Zero(t, svc.defaultFeedbackPenalty(ctx, nil, "fixture", 6031), "cooldown recovers without traffic")
	selection, _, err = svc.SelectAccountWithScheduler(ctx, nil, "", "", "fixture", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(6031), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	accounts[0].Schedulable = false
	selection, _, err = svc.SelectAccountWithScheduler(ctx, nil, "", "", "fixture", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(6032), selection.Account.ID, "feedback cannot bypass schedulability")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestIssue603TransportSemanticAnd403Feedback(t *testing.T) {
	svc := newOpenAIRejectedFieldTestService(&httpUpstreamRecorder{})
	clock := time.Now()
	svc.initOpenAIAccountScheduler()
	svc.openaiAccountStats.now = func() time.Time { return clock }
	account := newOpenAIRejectedFieldTestAccount()
	for range 5 {
		ctx, scope := withPriorityAttemptScope(context.Background(), "fixture", "/v1/responses")
		req, err := http.NewRequestWithContext(ctx, "POST", "http://synthetic.invalid/v1/responses", nil)
		require.NoError(t, err)
		observe := svc.observePriorityAttempt(req, account)
		clock = clock.Add(40 * time.Second)
		resp := priorityMockResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic\"}\n\ndata: {\"type\":\"response.completed\"}\n\n", true)
		observe(resp, nil)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		scope.finish(nil)
	}
	require.Greater(t, svc.defaultFeedbackPenalty(context.Background(), nil, "fixture", account.ID), 0.0)
	for _, tc := range []struct {
		body     string
		failures int64
	}{
		{`{"error":{"type":"server_error","message":"synthetic forbidden edge"}}`, 1},
		{`{"error":{"type":"permission_denied","message":"permission denied"}}`, 0},
	} {
		ctx, scope := withPriorityAttemptScope(context.Background(), tc.body, "/v1/responses")
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://synthetic.invalid/v1/responses", nil)
		observe := svc.observePriorityAttempt(req, account)
		resp := priorityMockResponse(403, tc.body, false)
		observe(resp, nil)
		_, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		scope.finish(nil)
		e := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: account.ID, model: tc.body, endpoint: "/v1/responses"}, clock, 10, 5)
		require.Equal(t, tc.failures, e.Failures)
	}
}
