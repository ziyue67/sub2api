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

func TestExcelBPS429DoesNotChangeCodexQuotaOrScheduling(t *testing.T) {
	for _, tc := range []struct {
		name         string
		headers      http.Header
		raw          string
		cancelOnRead bool
	}{
		{name: "quota headers", headers: excelBPSQuotaHeaders("100", "100")},
		{name: "unexhausted headers", headers: excelBPSQuotaHeaders("30", "20")},
		{name: "body reset timestamp", raw: fmt.Sprintf(`{"error":{"type":"usage_limit_reached","resets_at":%d}}`, time.Now().Add(2*time.Hour).Unix())},
		{name: "body reset duration", raw: `{"error":{"type":"usage_limit_reached","resets_in_seconds":7200}}`},
		{name: "generic rate limit"},
		{name: "malformed body", raw: "not json"},
		{name: "client cancellation", headers: excelBPSQuotaHeaders("100", "100"), cancelOnRead: true},
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

				require.EqualError(t, err, "excel BPS: basispoints_rate_limited")
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
				require.Equal(t, http.StatusTooManyRequests, rec.Code)
				require.True(t, IsResponseCommitted(c))
				require.Contains(t, rec.Body.String(), "basispoints_rate_limited")
				require.NotContains(t, rec.Body.String(), "PRIVATE_UPSTREAM")
				require.Len(t, upstream.requests, 1)
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
	for _, status := range []int{400, 401, 403, 500} {
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
