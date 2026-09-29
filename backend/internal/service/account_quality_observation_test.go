package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/stretchr/testify/require"
)

func bpsObservationConfig() *PelicanTestConfig {
	return &PelicanTestConfig{QuestionKind: "candy", TestChannel: "bps", Prompt: "Return 21", ReasoningEffort: "high", ParallelCount: 1,
		Quality: &QualityPolicy{Action: QualityActionObserveOnly, ExpectedAnswer: "21", Judge: &QualityJudgeConfig{GroupID: 9, ModelID: "judge", Prompt: "grade"}}}
}

func TestQualityBPSObservationValidation(t *testing.T) {
	plan := pelicanPlan()
	plan.PelicanConfig = bpsObservationConfig()
	q := plan.PelicanConfig.Quality
	q.AutoRestore, q.RemoveGroupIDs, q.BPS = true, []int64{3}, &QualityBPSPolicy{FailureThreshold: 2}
	_, err := nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	require.False(t, q.AutoRestore)
	require.Empty(t, q.RemoveGroupIDs)
	require.Nil(t, q.BPS)
	for _, action := range []string{QualityActionEnableBPS, "remove_groups", "disable_scheduling"} {
		q.Action = action
		_, err := nextPlanRun(plan, time.Now())
		require.ErrorContains(t, err, "observation-only")
	}
	q.Action = QualityActionObserveOnly
	plan.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
	_, err = nextPlanRun(plan, time.Now())
	require.ErrorContains(t, err, "candy question")
	plan.PelicanConfig = bpsObservationConfig()
	plan.PelicanConfig.TestChannel = "unknown"
	_, err = nextPlanRun(plan, time.Now())
	require.ErrorContains(t, err, "invalid test channel")
}

func TestQualityBPSObservationNeverFallsBackToNative(t *testing.T) {
	for _, change := range []func(*Account){
		func(a *Account) { a.Extra["openai_excel_bps"] = false },
		func(a *Account) { a.Extra["openai_excel_bps_models"] = []string{"gpt-5.6-sol"} },
		func(a *Account) { a.Type = AccountTypeAPIKey },
	} {
		account := excelAccount()
		change(account)
		upstream := &httpUpstreamRecorder{}
		svc := &AccountTestService{accountRepo: &stateProbeAccountRepo{account: account}, httpUpstream: upstream, openaiGatewayService: openAIClientToolsTestService(upstream)}
		result, err := svc.RunPelicanBackground(context.Background(), account.ID, "gpt-6-astra", bpsObservationConfig())
		require.NoError(t, err)
		require.Equal(t, "failed", result.Status)
		require.Contains(t, result.ErrorMessage, "BPS observation unavailable")
		require.Empty(t, upstream.requests)
		require.Equal(t, "inconclusive", qualityOutcome([]*ScheduledTestResult{result}))
	}
}

func TestQualityBPSObservationUsesBPSWithoutAccountActions(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			wire := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"21\"}]}]}}\n\n"
			wire = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"21\"}\n\n" + wire
			if status != http.StatusOK {
				wire = `{"error":{"code":"permission_denied"}}`
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}}
			account := excelAccount()
			account.Extra["openai_excel_bps_auto_disable_on_403"] = true
			account.Extra[ExcelBPSAutoMoveOn403Key], account.Extra[ExcelBPS403TargetGroupIDKey] = true, 7
			account.Credentials["model_mapping"] = map[string]any{"test-alias": "gpt-6-astra"}
			account.Extra["openai_excel_bps_models"] = []string{"gpt-6-astra"}
			gateway := openAIClientToolsTestService(upstream)
			mutations := 0
			mutate := func(context.Context, *Account) (bool, error) { mutations++; return true, nil }
			gateway.accountRepo = &excelBPSGroupActionRepo{excelBPSAutoDisableRepo: excelBPSAutoDisableRepo{disable: mutate}, move: mutate}
			svc := &AccountTestService{accountRepo: &stateProbeAccountRepo{account: account}, httpUpstream: upstream, openaiGatewayService: gateway}
			result, err := svc.RunPelicanBackground(context.Background(), account.ID, "test-alias", bpsObservationConfig())
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, basispoints.ResponsesURL, upstream.requests[0].URL.String())
			require.Zero(t, mutations, "observations must not disable BPS or move groups")
			require.False(t, gateway.isExcelBPSCoolingDown(account, "test-alias"))
			require.True(t, account.IsExcelBPSEnabled())
			require.True(t, account.IsExcelBPSAutoDisableOn403Enabled(), "shared account settings are preserved")
			require.Equal(t, true, account.Extra[ExcelBPSAutoMoveOn403Key])
			if status == http.StatusOK {
				require.Equal(t, "success", result.Status)
				require.Equal(t, "21", result.ResponseText)
			} else {
				require.Equal(t, "failed", result.Status)
			}
		})
	}
}

func TestQualityBPSObservationDoesNotDisableSchedulingOn401(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}}
	gateway := openAIClientToolsTestService(upstream)
	authRepo, invalidator := setupExcelBPSAuth(gateway)
	account := excelAccount()
	svc := &AccountTestService{accountRepo: &stateProbeAccountRepo{account: account}, httpUpstream: upstream, openaiGatewayService: gateway}
	result, err := svc.RunPelicanBackground(context.Background(), account.ID, "gpt-6-astra", bpsObservationConfig())
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Zero(t, authRepo.errorCalls+authRepo.tempCalls)
	require.Empty(t, invalidator.ids)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, account.IsExcelBPSEnabled())
	require.True(t, account.Schedulable)
}
