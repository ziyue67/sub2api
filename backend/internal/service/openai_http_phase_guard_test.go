package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func phaseGuardTestRequest(t *testing.T, svc *OpenAIGatewayService, c *gin.Context) *http.Request {
	t.Helper()
	ctx := svc.withOpenAIHTTPBudget(context.Background(), c)
	ctx, release := detachUpstreamContext(ctx)
	release()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://mock.invalid/v1/responses", strings.NewReader("mock"))
	require.NoError(t, err)
	return req
}

func TestOpenAIUploadDeadlineSurvivesDetachAndDoesNotAuthorizeReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 1}}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		account := &Account{ID: 1, Platform: PlatformOpenAI}
		req, guard := svc.guardOpenAIHTTPRequest(phaseGuardTestRequest(t, svc, c), account)
		defer guard.close()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		_, err := guard.finish(nil, req.Context().Err())
		var phaseErr *openAIHTTPPhaseTimeout
		require.ErrorAs(t, err, &phaseErr)
		require.Equal(t, "upload_timeout", phaseErr.phase)
		require.NotErrorIs(t, handleOpenAIHTTPPhaseError(c, account, err), context.Canceled)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
		require.Equal(t, http.StatusGatewayTimeout, c.Writer.Status())
	})
}

func TestOpenAIHTTPDeadlinesAreSharedAcrossAccountAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 2, OpenAIPreOutputBudgetSeconds: 3}}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		_, first := svc.guardOpenAIHTTPRequest(phaseGuardTestRequest(t, svc, c), &Account{ID: 1, Platform: PlatformOpenAI})
		time.Sleep(time.Second)
		first.close()
		req, next := svc.guardOpenAIHTTPRequest(phaseGuardTestRequest(t, svc, c), &Account{ID: 2, Platform: PlatformOpenAI})
		defer next.close()
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		var phaseErr *openAIHTTPPhaseTimeout
		require.ErrorAs(t, context.Cause(req.Context()), &phaseErr)
		require.Equal(t, "upload_timeout", phaseErr.phase, "second account must not get another full upload budget")
	})
}

func TestOpenAIHTTPUploadCompletionAndProgressDoNotCancelLongStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 1, OpenAIPreOutputBudgetSeconds: 2}}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req, guard := svc.guardOpenAIHTTPRequest(phaseGuardTestRequest(t, svc, c), &Account{Platform: PlatformOpenAI})
		defer guard.close()
		trace := httptrace.ContextClientTrace(req.Context())
		trace.WroteRequest(httptrace.WroteRequestInfo{})
		observeOpenAIHTTPProgress(req.Context())
		synctest.Wait()
		time.Sleep(time.Hour)
		require.NoError(t, req.Context().Err())
	})
}

func TestOpenAIHTTPEarlyResponseStopsUploadButNotPreOutputBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 1, OpenAIPreOutputBudgetSeconds: 2}}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req, guard := svc.guardOpenAIHTTPRequest(phaseGuardTestRequest(t, svc, c), &Account{Platform: PlatformOpenAI})
		resp, err := guard.finish(&http.Response{Request: req, Body: io.NopCloser(strings.NewReader(""))}, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, resp.Body.Close()) }()
		time.Sleep(1500 * time.Millisecond)
		require.NoError(t, req.Context().Err())
		time.Sleep(time.Second)
		synctest.Wait()
		var phaseErr *openAIHTTPPhaseTimeout
		require.ErrorAs(t, openAIHTTPPhaseError(resp), &phaseErr)
		require.Equal(t, "pre_output_budget", phaseErr.phase)
	})
}

func TestOpenAIForwardUploadTimeoutDoesNotResubmitAnExpiredBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Reuse the existing TLS-aware blocking mock; native HTTP calls Do.
		upstream := &blockingOpenAIResponseHeaderUpstream{canceled: make(chan struct{})}
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 1}}, httpUpstream: upstream}
		c, rec := newStreamLineageTestContext("phase-deadline")
		body := []byte(`{"model":"gpt-5","stream":true,"input":"hello"}`)
		account := newStreamLineageHTTPAccount()
		_, err := svc.Forward(context.Background(), c, account, body)
		var phaseErr *openAIHTTPPhaseTimeout
		require.ErrorAs(t, err, &phaseErr)
		require.Equal(t, "upload_timeout", phaseErr.phase)
		require.Equal(t, http.StatusGatewayTimeout, rec.Code)
		other := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}}
		svc.httpUpstream = other
		_, err = svc.Forward(context.Background(), c, account, body)
		require.ErrorAs(t, err, &phaseErr)
		require.Nil(t, other.lastReq, "expired request budget must reject before another network submission")
	})
}

func TestOpenAIUploadAccountOverrideAndDefaults(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 120}}}
	for _, tc := range []struct {
		value any
		want  time.Duration
	}{
		{float64(0), 0}, {float64(60), time.Minute}, {30, 30 * time.Second}, {float64(-1), 120 * time.Second}, {1.5, 120 * time.Second}, {"60", 120 * time.Second},
	} {
		require.Equal(t, tc.want, svc.openAIUploadTimeout(&Account{Extra: map[string]any{"openai_upload_timeout_seconds": tc.value}}))
	}
	request, err := http.NewRequest(http.MethodPost, "https://mock.invalid", nil)
	require.NoError(t, err)
	_, guard := (&OpenAIGatewayService{}).guardOpenAIHTTPRequest(request, &Account{Platform: PlatformOpenAI})
	require.Nil(t, guard)
}

func TestOpenAIHTTPBudgetKeepsOwnerCancellationAndClosesWatchers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 60}}}
		ctx, cancel := context.WithCancelCause(svc.withOpenAIHTTPBudget(context.Background(), nil))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://mock.invalid", nil)
		require.NoError(t, err)
		req, guard := svc.guardOpenAIHTTPRequest(req, &Account{Platform: PlatformOpenAI})
		ownerErr := errors.New("mock capacity owner revoked")
		cancel(ownerErr)
		synctest.Wait()
		require.ErrorIs(t, context.Cause(req.Context()), ownerErr)
		guard.close()
		select {
		case <-guard.done:
		default:
			t.Fatal("watcher must exit on owner cancellation")
		}
	})
}

func TestOpenAIPreOutputBudgetDoesNotRestartAfterResponseOrAccountSwitch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIPreOutputBudgetSeconds: 3}}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		firstReq, first := svc.guardOpenAIHTTPRequest(phaseGuardTestRequest(t, svc, c), &Account{Platform: PlatformOpenAI})
		response, err := first.finish(&http.Response{Request: firstReq, Body: io.NopCloser(strings.NewReader(""))}, nil)
		require.NoError(t, err)
		time.Sleep(2 * time.Second)
		require.NoError(t, response.Body.Close())
		nextReq, next := svc.guardOpenAIHTTPRequest(phaseGuardTestRequest(t, svc, c), &Account{ID: 2, Platform: PlatformOpenAI})
		defer next.close()
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		var phaseErr *openAIHTTPPhaseTimeout
		require.ErrorAs(t, context.Cause(nextReq.Context()), &phaseErr)
		require.Equal(t, "pre_output_budget", phaseErr.phase)
	})
}

func TestSIWCEgressKeepsHTTPPhaseDeadlines(t *testing.T) {
	for _, tc := range []struct {
		name    string
		gateway config.GatewayConfig
		phase   string
	}{
		{name: "upload", gateway: config.GatewayConfig{OpenAIUploadTimeoutSeconds: 1}, phase: "upload_timeout"},
		{name: "pre-output", gateway: config.GatewayConfig{OpenAIPreOutputBudgetSeconds: 1}, phase: "pre_output_budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				upstream := &blockingOpenAIResponseHeaderUpstream{canceled: make(chan struct{})}
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: tc.gateway}, httpUpstream: upstream}
				req, err := http.NewRequestWithContext(svc.withOpenAIHTTPBudget(context.Background(), nil), http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"input":"hello"}`))
				require.NoError(t, err)
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{OpenAISIWCEnabledKey: true}}
				_, err = svc.doOpenAIUpstream(req, "", account)
				var phaseErr *openAIHTTPPhaseTimeout
				require.ErrorAs(t, err, &phaseErr)
				require.Equal(t, tc.phase, phaseErr.phase)
				select {
				case <-upstream.canceled:
				default:
					t.Fatal("SIWC upstream must be canceled by the shared HTTP deadline")
				}
			})
		})
	}
}
