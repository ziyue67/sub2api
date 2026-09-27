package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type excelBPSQuotaWrite struct {
	accountID int64
	updates   map[string]any
	resetAt   time.Time
	ctxErr    error
	deadline  time.Time
}

type excelBPSQuotaRepo struct {
	AccountRepository
	writes    chan excelBPSQuotaWrite
	updateErr error
	limitErr  error
}

type excelBPSQuotaSettingsRepo struct {
	SettingRepository
	cooldown string
}

func (r *excelBPSQuotaSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == SettingKeyRateLimit429CooldownSettings {
		return r.cooldown, nil
	}
	return "", ErrSettingNotFound
}

func (r *excelBPSQuotaRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	copied := make(map[string]any, len(updates))
	for key, value := range updates {
		copied[key] = value
	}
	deadline, _ := ctx.Deadline()
	r.writes <- excelBPSQuotaWrite{accountID: id, updates: copied, ctxErr: ctx.Err(), deadline: deadline}
	return r.updateErr
}

func (r *excelBPSQuotaRepo) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	deadline, _ := ctx.Deadline()
	r.writes <- excelBPSQuotaWrite{accountID: id, resetAt: resetAt, ctxErr: ctx.Err(), deadline: deadline}
	return r.limitErr
}

func nextExcelBPSQuotaWrite(t *testing.T, repo *excelBPSQuotaRepo) excelBPSQuotaWrite {
	t.Helper()
	select {
	case write := <-repo.writes:
		return write
	case <-time.After(3 * time.Second):
		t.Fatal("expected an account quota update")
		return excelBPSQuotaWrite{}
	}
}

func requireNoExcelBPSQuotaWrite(t *testing.T, repo *excelBPSQuotaRepo) {
	t.Helper()
	select {
	case write := <-repo.writes:
		t.Fatalf("unexpected account quota update: %+v", write)
	case <-time.After(20 * time.Millisecond):
	}
}

func excelBPSQuotaHeaders(used5h, used7d string) http.Header {
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", used7d)
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", used5h)
	headers.Set("x-codex-secondary-reset-after-seconds", "18000")
	headers.Set("x-codex-secondary-window-minutes", "300")
	return headers
}

func TestExcelBPSQuotaSnapshotAtResponseBoundary(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, brokenStream := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/broken=%t", stream, brokenStream), func(t *testing.T) {
				wire := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bps_quota\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n"
				if brokenStream {
					wire = ""
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: excelBPSQuotaHeaders("63", "28"), Body: io.NopCloser(strings.NewReader(wire)),
				}}
				svc := openAIClientToolsTestService(upstream)
				svc.codexSnapshotThrottle = newAccountWriteThrottle(time.Hour)
				repo := &excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}
				svc.accountRepo = repo
				account := excelAccount()
				body := []byte(fmt.Sprintf("{\"model\":\"gpt-6-astra\",\"input\":\"test\",\"stream\":%t}", stream))
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				_, err := svc.Forward(context.Background(), c, account, body)
				if brokenStream {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				write := nextExcelBPSQuotaWrite(t, repo)
				require.Equal(t, account.ID, write.accountID)
				require.Equal(t, 63.0, write.updates["codex_5h_used_percent"])
				require.Equal(t, 28.0, write.updates["codex_7d_used_percent"])
				updatedAtValue, ok := write.updates["codex_usage_updated_at"].(string)
				require.True(t, ok)
				updatedAt, err := time.Parse(time.RFC3339, updatedAtValue)
				require.NoError(t, err)
				require.WithinDuration(t, time.Now(), updatedAt, 5*time.Second)
				require.NoError(t, write.ctxErr)
				require.True(t, write.resetAt.IsZero())
				require.NotContains(t, account.Extra, "codex_usage_updated_at", "do not mutate a shared account snapshot")
				// A second response still succeeds but uses the existing snapshot throttle.
				upstream.resp.Body = io.NopCloser(strings.NewReader(wire))
				c, _ = gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				_, _ = svc.Forward(context.Background(), c, account, body)
				requireNoExcelBPSQuotaWrite(t, repo)
			})
		}
	}
}

func TestExcelBPSQuotaSnapshotRequiresHeaders(t *testing.T) {
	for _, headers := range []http.Header{nil, {"X-Codex-Primary-Used-Percent": {"invalid"}}} {
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(""))}}
		svc := openAIClientToolsTestService(upstream)
		svc.codexSnapshotThrottle = newAccountWriteThrottle(time.Hour)
		repo := &excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}
		svc.accountRepo = repo
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		_, _ = svc.Forward(context.Background(), c, excelAccount(), []byte("{\"model\":\"gpt-6-astra\",\"input\":\"test\"}"))
		requireNoExcelBPSQuotaWrite(t, repo)
	}
}

type excelBPSCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *excelBPSCancelReader) Read(p []byte) (int, error) {
	r.cancel()
	return r.Reader.Read(p)
}

// requireExcelBPSRateLimitFailover checks that a BPS 429 left the response to
// the handler's account switch and kept upstream content out of the error.
func requireExcelBPSRateLimitFailover(t *testing.T, err error, c *gin.Context) {
	t.Helper()
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusTooManyRequests, failover.StatusCode)
	require.Equal(t, ExcelBPSRateLimitedReason, failover.Reason)
	require.Equal(t, http.StatusTooManyRequests, failover.ClientStatusCode)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.RetryableOnSameAccount, "do not retry the throttled account in this request")
	require.False(t, failover.ShouldReportAccountScheduleFailure(), "keep the shared scheduler score")
	require.Empty(t, failover.ResponseBody)
	for key := range failover.ResponseHeaders {
		require.Equal(t, "Retry-After", key)
	}
	require.False(t, c.Writer.Written(), "leave the response to the handler")
	require.False(t, IsResponseCommitted(c))
}

func excelBPSCooldownRemaining(t *testing.T, svc *OpenAIGatewayService, accountID int64) time.Duration {
	t.Helper()
	value, ok := svc.excelBPSCooldownUntil.Load(accountID)
	require.True(t, ok, "expected a BPS cooldown")
	until, ok := value.(time.Time)
	require.True(t, ok)
	return time.Until(until)
}

func TestExcelBPS429FailsOverWithoutChangingCodexState(t *testing.T) {
	retryAt := time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
	for _, tc := range []struct {
		name           string
		headers        http.Header
		raw            string
		cancelOnRead   bool
		wantRetryAfter string
		wantCooldown   time.Duration
	}{
		{name: "quota headers", headers: excelBPSQuotaHeaders("100", "100"), wantCooldown: 11 * time.Second},
		{name: "unexhausted headers", headers: excelBPSQuotaHeaders("30", "20"), wantCooldown: 11 * time.Second},
		{name: "body reset timestamp", raw: fmt.Sprintf(`{"error":{"type":"usage_limit_reached","resets_at":%d}}`, time.Now().Add(2*time.Hour).Unix()), wantCooldown: 11 * time.Second},
		{name: "body reset duration", raw: `{"error":{"type":"usage_limit_reached","resets_in_seconds":7200}}`, wantCooldown: 11 * time.Second},
		{name: "generic rate limit", wantCooldown: 11 * time.Second},
		{name: "malformed body", raw: "not json", wantCooldown: 11 * time.Second},
		{name: "retry after seconds", headers: http.Header{"Retry-After": {"30"}}, wantRetryAfter: "30", wantCooldown: 30 * time.Second},
		{name: "retry after date", headers: http.Header{"Retry-After": {retryAt}}, wantRetryAfter: retryAt, wantCooldown: time.Minute},
		{name: "retry after beyond cap", headers: http.Header{"Retry-After": {"86400"}}, wantRetryAfter: "86400", wantCooldown: 2 * time.Hour},
		{name: "invalid retry after", headers: http.Header{"Retry-After": {"soon"}}, wantCooldown: 11 * time.Second},
		{name: "client cancellation", headers: excelBPSQuotaHeaders("100", "100"), cancelOnRead: true, wantCooldown: 11 * time.Second},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				raw := tc.raw
				if raw == "" {
					raw = `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"PRIVATE_UPSTREAM"}}`
				}
				var reader io.Reader = strings.NewReader(raw)
				if tc.cancelOnRead {
					reader = &excelBPSCancelReader{Reader: reader, cancel: cancel}
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusTooManyRequests, Header: tc.headers, Body: io.NopCloser(reader)}}
				svc := openAIClientToolsTestService(upstream)
				repo := &excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}
				svc.accountRepo = repo
				svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
				svc.rateLimitService.SetSettingService(NewSettingService(&excelBPSQuotaSettingsRepo{cooldown: `{"enabled":true,"cooldown_seconds":11}`}, svc.cfg))
				svc.rateLimitService.SetAccountRuntimeBlocker(svc)
				account := excelAccount()
				account.Extra["openai_excel_bps_auto_disable_on_403"] = true
				retryStarted := time.Now()
				svc.openaiOAuth429RetryStartedAt.Store(account.ID, retryStarted)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","input":"test","stream":%t}`, stream))

				_, err := svc.Forward(ctx, c, account, body)

				if tc.cancelOnRead {
					require.ErrorIs(t, err, context.Canceled, "a gone client must not start a failover")
					var failover *UpstreamFailoverError
					require.NotErrorAs(t, err, &failover)
					marked, ok := GetOpsStreamError(c)
					require.True(t, ok)
					require.Equal(t, OpsClientCanceledCode, marked.Code)
					require.False(t, c.Writer.Written())
				} else {
					requireExcelBPSRateLimitFailover(t, err, c)
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.Equal(t, tc.wantRetryAfter, failover.ResponseHeaders.Get("Retry-After"))
				}
				require.Len(t, upstream.requests, 1)
				require.NotContains(t, rec.Body.String(), "PRIVATE_UPSTREAM")
				_, finalUpstreamError := c.Get(OpsUpstreamStatusCodeKey)
				require.False(t, finalUpstreamError, "a failover attempt is not the final upstream error")
				rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
				require.True(t, ok)
				events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
				require.True(t, ok)
				require.Len(t, events, 1)
				require.Equal(t, "failover", events[0].Kind)
				require.Equal(t, http.StatusTooManyRequests, events[0].UpstreamStatusCode)
				require.NotContains(t, events[0].Message+events[0].Detail, "PRIVATE_UPSTREAM")

				// The observed 429 cools only the BPS route, even when the client left.
				require.InDelta(t, tc.wantCooldown.Seconds(), excelBPSCooldownRemaining(t, svc, account.ID).Seconds(), 2)
				require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false))
				requireNoExcelBPSQuotaWrite(t, repo)
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
				require.True(t, account.IsSchedulable())
				require.True(t, account.IsExcelBPSEnabled())
				require.Nil(t, account.RateLimitResetAt)
				retryState, ok := svc.openaiOAuth429RetryStartedAt.Load(account.ID)
				require.True(t, ok)
				require.Equal(t, retryStarted, retryState)
			})
		}
	}
}

func TestExcelBPSNon429DoesNotChangeQuotaState(t *testing.T) {
	// Authentication failures have their own scheduling assertions.
	for _, status := range []int{400, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: status, Header: excelBPSQuotaHeaders("100", "100"), Body: io.NopCloser(strings.NewReader("{}"))}}
			svc := openAIClientToolsTestService(upstream)
			repo := &excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}
			svc.accountRepo = repo
			svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
			svc.rateLimitService.SetAccountRuntimeBlocker(svc)
			account := excelAccount()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			_, err := svc.Forward(context.Background(), c, account, []byte("{\"model\":\"gpt-6-astra\",\"input\":\"test\"}"))
			require.Error(t, err)
			require.Equal(t, status, rec.Code)
			requireNoExcelBPSQuotaWrite(t, repo)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			require.True(t, account.IsSchedulable())
		})
	}
}

func TestExcelBPS429FallbackCooldownFollowsSetting(t *testing.T) {
	for _, tc := range []struct {
		name         string
		setting      string
		noRateLimit  bool
		retryAfter   string
		wantCooldown time.Duration
	}{
		{name: "configured seconds", setting: `{"enabled":true,"cooldown_seconds":11}`, wantCooldown: 11 * time.Second},
		{name: "disabled without retry after", setting: `{"enabled":false,"cooldown_seconds":11}`},
		{name: "retry after wins when disabled", setting: `{"enabled":false,"cooldown_seconds":11}`, retryAfter: "20", wantCooldown: 20 * time.Second},
		{name: "zero retry after keeps a minimum", setting: `{"enabled":true,"cooldown_seconds":11}`, retryAfter: "0", wantCooldown: time.Second},
		{name: "no rate limit service", noRateLimit: true, wantCooldown: defaultRateLimit429CooldownSeconds * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.retryAfter != "" {
				header.Set("Retry-After", tc.retryAfter)
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusTooManyRequests, Header: header, Body: io.NopCloser(strings.NewReader("{}"))}}
			svc := openAIClientToolsTestService(upstream)
			if !tc.noRateLimit {
				repo := &excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}
				svc.accountRepo = repo
				svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
				svc.rateLimitService.SetSettingService(NewSettingService(&excelBPSQuotaSettingsRepo{cooldown: tc.setting}, svc.cfg))
			}
			account := excelAccount()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","input":"test"}`))

			requireExcelBPSRateLimitFailover(t, err, c)
			if tc.wantCooldown == 0 {
				_, cooling := svc.excelBPSCooldownUntil.Load(account.ID)
				require.False(t, cooling, "a disabled fallback only relies on this request's exclusion")
				return
			}
			require.InDelta(t, tc.wantCooldown.Seconds(), excelBPSCooldownRemaining(t, svc, account.ID).Seconds(), 1)
		})
	}
}

func TestExcelBPSCooldownNeverShortensAndExpires(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := excelAccount()
	svc.coolDownExcelBPS(context.Background(), account, "60")
	svc.coolDownExcelBPS(context.Background(), account, "5")
	require.InDelta(t, 60, excelBPSCooldownRemaining(t, svc, account.ID).Seconds(), 1, "a shorter cooldown must not win")
	svc.coolDownExcelBPS(context.Background(), account, "120")
	require.InDelta(t, 120, excelBPSCooldownRemaining(t, svc, account.ID).Seconds(), 1)

	other := excelAccount()
	other.ID++
	var wg sync.WaitGroup
	for i := 1; i <= 64; i++ {
		wg.Add(1)
		go func(seconds int) {
			defer wg.Done()
			svc.coolDownExcelBPS(context.Background(), other, strconv.Itoa(seconds))
		}(i * 10)
	}
	wg.Wait()
	require.InDelta(t, 640, excelBPSCooldownRemaining(t, svc, other.ID).Seconds(), 1, "concurrent writes keep the longest cooldown")

	svc.excelBPSCooldownUntil.Store(account.ID, time.Now().Add(-time.Second))
	require.False(t, svc.isExcelBPSCoolingDown(account, "gpt-6-astra"))
	_, stale := svc.excelBPSCooldownUntil.Load(account.ID)
	require.False(t, stale, "expired cooldowns are dropped")
}

func TestExcelBPSCooldownOnlyAffectsBPSRoutedModels(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := excelAccount()
	account.Extra["openai_excel_bps_models"] = []any{"gpt-6-astra"}
	svc.coolDownExcelBPS(context.Background(), account, "30")

	require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false))
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", true))
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.1", false), "native Codex models stay schedulable")
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account), "no account-wide block")

	account.Extra["openai_excel_bps"] = false
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false), "once BPS is off the model is native")
}

func TestExcelBPS429AfterOutputCoolsWithoutReplay(t *testing.T) {
	for _, route := range []string{"tool correction", "unknown tool regeneration"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", route, stream), func(t *testing.T) {
				summary, code := "Run", "text(42);"
				body := []byte(fmt.Sprintf(`{"model":"gpt-5.6-sol","stream":%t,"input":"test","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]}`, stream))
				if route == "unknown tool regeneration" {
					summary, code = "Call client tool", `{"name":"unknown","arguments":{}}`
					body = []byte(fmt.Sprintf(`{"model":"gpt-5.6-sol","stream":%t,"input":"test","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}]}`, stream))
				}
				first := &excelBPSRepairBody{Reader: strings.NewReader(excelBPSRepairWire(t, "throttled", summary, code))}
				rejected := &excelBPSRepairBody{Reader: strings.NewReader(`{"error":{"message":"PRIVATE_UPSTREAM"}}`)}
				retryAfter := excelBPSQuotaHeaders("100", "100")
				retryAfter.Set("Retry-After", "45")
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					{StatusCode: http.StatusOK, Header: http.Header{}, Body: first},
					{StatusCode: http.StatusTooManyRequests, Header: retryAfter, Body: rejected},
				}}
				svc := openAIClientToolsTestService(upstream)
				repo := &excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}
				svc.accountRepo = repo
				svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
				svc.rateLimitService.SetAccountRuntimeBlocker(svc)
				account := excelAccount()
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

				_, err := svc.Forward(context.Background(), c, account, body)

				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover, "accepted output must not be replayed on another account")
				require.Len(t, upstream.requests, 2)
				require.True(t, rejected.closed.Load())
				require.NotContains(t, rec.Body.String(), "PRIVATE_UPSTREAM")
				require.InDelta(t, 45, excelBPSCooldownRemaining(t, svc, account.ID).Seconds(), 2)
				requireNoExcelBPSQuotaWrite(t, repo)
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			})
		}
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_ExcelBPSCooldownSkipsOnlyBPSRoutes(t *testing.T) {
	for _, advanced := range []bool{true, false} {
		t.Run(fmt.Sprintf("advanced=%t", advanced), func(t *testing.T) {
			ctx := context.Background()
			groupID := int64(101301)
			newAccount := func(id int64) Account {
				return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"access_token": "token", "chatgpt_account_id": "chatgpt"},
					Extra:       map[string]any{"openai_excel_bps": true, "openai_excel_bps_models": []any{"gpt-6-astra"}}}
			}
			accounts := []Account{newAccount(38301), newAccount(38302)}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = true
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:              &schedulerTestGatewayCache{},
				cfg:                cfg,
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService(strconv.FormatBool(advanced)),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			require.Equal(t, advanced, svc.isOpenAIAdvancedSchedulerEnabled(ctx))
			selectModel := func(model string) (*AccountSelectionResult, error) {
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", model, nil, OpenAIUpstreamTransportAny, false)
				if selection != nil && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				return selection, err
			}

			svc.coolDownExcelBPS(ctx, &accounts[0], "60")
			for range 10 {
				selection, err := selectModel("gpt-6-astra")
				require.NoError(t, err)
				require.Equal(t, int64(38302), selection.Account.ID)
			}

			svc.coolDownExcelBPS(ctx, &accounts[1], "60")
			_, err := selectModel("gpt-6-astra")
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Contains(t, err.Error(), excelBPSRateLimitedFilterReason+"=2")
			selection, err := selectModel("gpt-5.1")
			require.NoError(t, err, "models these accounts forward natively stay schedulable")
			require.NotNil(t, selection.Account)
		})
	}
}
