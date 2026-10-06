package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qualityqueue"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qualityqueue/queuetest"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func qualitySignalTestLimiter(t *testing.T) (*RateLimitService, *miniredis.Miniredis) {
	t.Helper()
	r := miniredis.RunT(t)
	client := queuetest.NewClient(r.Addr())
	t.Cleanup(func() { _ = client.Close() })
	queue := qualityqueue.NewRedis(client)
	t.Cleanup(func() { _ = queue.Close() })
	return &RateLimitService{qualityTrigger: &quality5xxTrigger{queue: queue}}, r
}

func TestQuality5xxSemanticStreams(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name, payload string
			want          bool
		}{
			{"overload", `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"overloaded"}}}`, true},
			{"request_policy_400", `{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":"Invalid prompt: policy rejection"}}}`, false},
			{"request_permission", `{"type":"response.failed","response":{"error":{"type":"permission_error","message":"forbidden content"}}}`, false},
		} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", tc.name, passthrough), func(t *testing.T) {
				limiter, client := qualitySignalTestLimiter(t)
				svc := openAIClientToolsTestService(nil)
				svc.rateLimitService = limiter
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				account := &Account{ID: 417, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + tc.payload + "\n\n"))}
				if passthrough {
					_, _ = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.5", "gpt-5.5")
				} else {
					_, _ = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.5", "gpt-5.5")
				}
				require.Equal(t, tc.want, client.Exists(quality5xxPendingKey))
			})
		}
	}
}

func TestQuality5xxSemanticProbeDoesNotRecurse(t *testing.T) {
	limiter, client := qualitySignalTestLimiter(t)
	svc := &OpenAIGatewayService{rateLimitService: limiter}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(context.Background(), qualityProbeContextKey{}, true))
	svc.recordOpenAIStreamUpstreamError(c, &Account{ID: 417, Type: AccountTypeOAuth}, false, "", "stream_failed", []byte(`{"type":"response.failed"}`), "failed")
	require.Zero(t, len(client.Keys()))
}

func TestQuality5xxWebsocketSemanticFailure(t *testing.T) {
	for _, event := range []string{"error", "response.failed"} {
		t.Run(event, func(t *testing.T) {
			limiter, client := qualitySignalTestLimiter(t)
			svc := &OpenAIGatewayService{rateLimitService: limiter}
			account := &Account{ID: 417, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			payload := []byte(`{"type":"error","error":{"code":"invalid_prompt","message":"policy rejection"}}`)
			if event == "error" {
				svc.handleOpenAIWSErrorEventTransientFailure(context.Background(), account, "gpt-5.5", nil, payload)
			} else {
				svc.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-5.5", nil, payload)
			}
			require.Equal(t, []string{"417"}, qualityTestMembers(t, client))
		})
	}
}

func TestQuality5xxBPSStreamFailure(t *testing.T) {
	limiter, client := qualitySignalTestLimiter(t)
	wire := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"overloaded\"}}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}}
	svc := openAIClientToolsTestService(upstream)
	svc.rateLimitService = limiter
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	account := excelAccount()
	_, err := svc.Forward(c.Request.Context(), c, account, []byte(`{"model":"gpt-6-astra","stream":true,"input":"hi"}`))
	require.Error(t, err)
	require.Equal(t, []string{fmt.Sprint(account.ID)}, qualityTestMembers(t, client))
}

func TestQuality5xxChoosesProbeOrCandyWithoutChangingRule(t *testing.T) {
	for _, bps := range []bool{false, true} {
		for _, recovery := range []bool{false, true} {
			t.Run(fmt.Sprintf("bps=%t/recovery=%t", bps, recovery), func(t *testing.T) {
				plans, results := &qualityTriggerPlans{}, &pelicanResults{}
				account := stateProbeAccount()
				if bps {
					account.Extra = map[string]any{"openai_excel_bps": true}
				}
				plan := pelicanPlan()
				plan.TriggerSource = quality5xxSource
				plan.PelicanConfig = stateProbePlanConfig()
				plan.PelicanConfig.Quality = &QualityPolicy{Action: "disable_scheduling", TriggerOnUpstream5xx: true}
				if recovery {
					plan.PelicanConfig.Quality.Action = QualityActionEnableBPS
					plan.PelicanConfig.Quality.BPS = &QualityBPSPolicy{FailureThreshold: 1, AllModels: true}
				}
				original := plan.PelicanConfig
				runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, results), accountTestSvc: &AccountTestService{accountRepo: &stateProbeAccountRepo{account: account}}}
				calls := 0
				runner.runPelican = func(_ context.Context, _ int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
					calls++
					if bps {
						require.Equal(t, "candy", cfg.QuestionKind)
						require.Equal(t, CandyPrompt, cfg.Prompt)
						return &ScheduledTestResult{Status: "success", ResponseText: "21"}, nil
					}
					require.Equal(t, OpenAICodexStateProbeQuestionKind, cfg.QuestionKind)
					return &ScheduledTestResult{Status: "success", QualityJudgment: &QualityJudgment{Verdict: "correct"}}, nil
				}
				require.True(t, runner.runOnePlan(context.Background(), plan))
				require.Equal(t, 1, calls)
				require.Len(t, results.results, 1)
				require.Equal(t, "success", results.results[0].Status)
				require.Equal(t, OpenAICodexStateProbeQuestionKind, original.QuestionKind)
				require.Empty(t, original.Quality.ExpectedAnswer)
				if bps && recovery {
					require.Zero(t, plans.outcomes, "BPS candy is not evidence of direct recovery")
				} else {
					require.Equal(t, 1, plans.outcomes)
				}
			})
		}
	}
}

type qualityRecentPlanRepo struct {
	qualityTriggerPlans
	plan *ScheduledTestPlan
}

func (r *qualityRecentPlanRepo) ListByAccountID(context.Context, int64) ([]*ScheduledTestPlan, error) {
	return []*ScheduledTestPlan{r.plan}, nil
}

func TestQuality5xxDoesNotSkipRecentlyCompletedPlan(t *testing.T) {
	plan := pelicanPlan()
	recent, future := time.Now(), time.Now().Add(time.Hour)
	plan.LastRunAt, plan.NextRunAt = &recent, &future
	plan.PelicanConfig = stateProbePlanConfig()
	plan.PelicanConfig.Quality = &QualityPolicy{Action: "disable_scheduling", TriggerOnUpstream5xx: true}
	plans, results := &qualityRecentPlanRepo{plan: plan}, &pelicanResults{}
	runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, results)}
	runner.runPelican = func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
		return &ScheduledTestResult{Status: "success", QualityJudgment: &QualityJudgment{Verdict: "correct"}}, nil
	}
	require.NoError(t, runner.runQualityTriggeredAccount(context.Background(), plan.AccountID, time.Now()))
	require.Len(t, results.results, 1)
	require.True(t, future.Equal(plans.next), "event test must keep the future cron")
}

func TestQuality5xxCoalescesWithOverlappingCron(t *testing.T) {
	plan := pelicanPlan()
	plan.PelicanConfig = stateProbePlanConfig()
	plan.PelicanConfig.Quality = &QualityPolicy{Action: "disable_scheduling", TriggerOnUpstream5xx: true}
	observed, until := time.Now(), time.Now().Add(time.Minute)
	plan.RunningUntil = &until
	plans := &qualityRecentPlanRepo{plan: plan}
	runner := &ScheduledTestRunnerService{planRepo: plans}
	require.Error(t, runner.runQualityTriggeredAccount(context.Background(), plan.AccountID, observed))
	finished := observed.Add(time.Second)
	plan.LastRunAt, plan.RunningUntil = &finished, nil
	require.NoError(t, runner.runQualityTriggeredAccount(context.Background(), plan.AccountID, observed))
	require.False(t, plans.claimed, "cron completion must consume the old signal without a second test")
}

func qualityTestMembers(t *testing.T, server *miniredis.Miniredis) []string {
	t.Helper()
	members, err := server.ZMembers(quality5xxPendingKey)
	require.NoError(t, err)
	return members
}
