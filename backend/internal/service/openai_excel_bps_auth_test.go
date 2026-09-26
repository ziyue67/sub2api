package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

type excelBPSAuthRepo struct {
	excelBPSQuotaRepo
	errorCalls, tempCalls int
	accountID             int64
	reason                string
	until                 time.Time
	ctxErr                error
	deadline              time.Time
	writeErr              error
}

func (r *excelBPSAuthRepo) record(ctx context.Context, id int64, reason string) error {
	r.accountID, r.reason, r.ctxErr = id, reason, ctx.Err()
	r.deadline, _ = ctx.Deadline()
	return r.writeErr
}

func (r *excelBPSAuthRepo) SetError(ctx context.Context, id int64, reason string) error {
	r.errorCalls++
	return r.record(ctx, id, reason)
}

func (r *excelBPSAuthRepo) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.tempCalls++
	r.until = until
	return r.record(ctx, id, reason)
}

type excelBPSAuthInvalidator struct{ ids []int64 }

func (r *excelBPSAuthInvalidator) InvalidateToken(_ context.Context, account *Account) error {
	r.ids = append(r.ids, account.ID)
	return nil
}

func setupExcelBPSAuth(svc *OpenAIGatewayService) (*excelBPSAuthRepo, *excelBPSAuthInvalidator) {
	repo := &excelBPSAuthRepo{excelBPSQuotaRepo: excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}}
	invalidator := &excelBPSAuthInvalidator{}
	svc.accountRepo = repo
	svc.cfg.RateLimit.OAuth401CooldownMinutes = 7
	svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
	svc.rateLimitService.SetTokenCacheInvalidator(invalidator)
	svc.rateLimitService.SetAccountRuntimeBlocker(svc)
	return repo, invalidator
}

func TestExcelBPS401PersistsAfterCancellationAndBlocksOnWriteFailure(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		for _, writeFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("permanent=%t/writeFailure=%t", permanent, writeFailure), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				raw := `{}`
				if permanent {
					raw = `{"error":{"code":"token_revoked"}}`
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(&excelBPSCancelReader{Reader: strings.NewReader(raw), cancel: cancel})}}
				svc := openAIClientToolsTestService(upstream)
				repo, _ := setupExcelBPSAuth(svc)
				if writeFailure {
					repo.writeErr = errors.New("database unavailable")
				}
				account := excelAccount()
				account.Credentials["refresh_token"] = "refresh-secret"
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				_, err := svc.Forward(ctx, c, account, []byte(`{"model":"gpt-5.6-sol","input":"test"}`))
				require.Error(t, err)
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				require.NoError(t, repo.ctxErr)
				require.Equal(t, 1, repo.tempCalls+repo.errorCalls)
				require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
			})
		}
	}
}

func TestExcelBPS401RespectsAPIKeyPolicies(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pool, custom bool
		codes        []any
		blocked      bool
	}{
		{name: "API key", blocked: true},
		{name: "pool", pool: true},
		{name: "custom excludes 401", custom: true, codes: []any{float64(403)}},
		{name: "custom includes 401", custom: true, codes: []any{float64(401)}, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := openAIClientToolsTestService(nil)
			repo, invalidator := setupExcelBPSAuth(svc)
			account := excelAccount()
			account.Type = AccountTypeAPIKey
			account.Credentials["pool_mode"] = tc.pool
			account.Credentials["custom_error_codes_enabled"] = tc.custom
			account.Credentials["custom_error_codes"] = tc.codes
			svc.handleExcelBPSUnauthorized(context.Background(), account, 401, http.Header{}, []byte(`{}`))
			require.Equal(t, tc.blocked, svc.isOpenAIAccountRuntimeBlocked(account))
			require.Equal(t, tc.blocked, repo.errorCalls == 1)
			require.Zero(t, repo.tempCalls)
			require.Empty(t, invalidator.ids)
			requireNoExcelBPSQuotaWrite(t, &repo.excelBPSQuotaRepo)
		})
	}
}

func TestExcelBPS401AppliesAuthenticationPolicy(t *testing.T) {
	for _, route := range []string{"responses", "attachment", "tool correction", "unknown tool regeneration"} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, raw                        string
				noRefresh, permanent, invalidate bool
			}{
				{name: "expired", raw: `{"error":{"message":"echo test-token refresh-secret PRIVATE_REQUEST file-private"}}`, invalidate: true},
				{name: "invalidated", raw: `{"error":{"code":"token_invalidated","message":"PRIVATE_REQUEST"}}`, permanent: true},
				{name: "revoked", raw: `{"error":{"code":"token_revoked"}}`, permanent: true},
				{name: "unauthorized detail", raw: `{"detail":"Unauthorized"}`, permanent: true},
				{name: "missing refresh token", raw: `{}`, noRefresh: true, permanent: true, invalidate: true},
				{name: "non JSON", raw: `PRIVATE_REQUEST test-token`, invalidate: true},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", route, stream, tc.name), func(t *testing.T) {
					rejected := &excelBPSRepairBody{Reader: strings.NewReader(tc.raw)}
					upstream := &httpUpstreamRecorder{}
					svc := openAIClientToolsTestService(upstream)
					repo, invalidator := setupExcelBPSAuth(svc)
					account := excelAccount()
					if !tc.noRefresh {
						account.Credentials["refresh_token"] = "refresh-secret"
					}
					account.Extra["openai_excel_bps_auto_disable_on_403"] = true
					body := []byte(fmt.Sprintf(`{"model":"gpt-5.6-sol","stream":%t,"input":"test"}`, stream))
					wantRequests := 1
					var first *excelBPSRepairBody
					switch route {
					case "attachment":
						enableNativeAttachments(svc)
						body, _ = nativeGatewayBody(t)
						var err error
						body, err = sjson.SetBytes(body, "stream", stream)
						require.NoError(t, err)
					case "tool correction", "unknown tool regeneration":
						wantRequests = 2
						summary, code := "Run", "text(42);"
						body = []byte(fmt.Sprintf(`{"model":"gpt-5.6-sol","stream":%t,"input":"test","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}]}`, stream))
						if route == "unknown tool regeneration" {
							summary, code = "Call client tool", `{"name":"unknown","arguments":{}}`
							body = []byte(fmt.Sprintf(`{"model":"gpt-5.6-sol","stream":%t,"input":"test","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}]}`, stream))
						}
						first = &excelBPSRepairBody{Reader: strings.NewReader(excelBPSRepairWire(t, "auth", summary, code))}
						upstream.responses = append(upstream.responses, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: first})
					}
					upstream.responses = append(upstream.responses, &http.Response{StatusCode: http.StatusUnauthorized, Header: excelBPSQuotaHeaders("100", "100"), Body: rejected})
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					started := time.Now()
					_, err := svc.Forward(context.Background(), c, account, body)
					require.Error(t, err)
					var failover *UpstreamFailoverError
					require.NotErrorAs(t, err, &failover, "authentication state updates must not replay BPS requests")
					require.Len(t, upstream.requests, wantRequests)
					require.True(t, rejected.closed.Load())
					if first != nil {
						require.True(t, first.closed.Load())
					}
					require.True(t, IsResponseCommitted(c))
					if wantRequests == 1 {
						require.Equal(t, http.StatusUnauthorized, rec.Code)
						require.Contains(t, rec.Body.String(), "authentication failed")
					}
					require.NotContains(t, rec.Body.String(), "scheduling was not changed")
					require.Equal(t, account.ID, repo.accountID)
					require.NoError(t, repo.ctxErr)
					require.False(t, repo.deadline.IsZero())
					require.LessOrEqual(t, repo.deadline.Sub(started), openAIAccountStateUpdateTimeout+time.Second)
					if tc.permanent {
						require.Equal(t, 1, repo.errorCalls)
						require.Zero(t, repo.tempCalls)
					} else {
						require.Equal(t, 1, repo.tempCalls)
						require.Zero(t, repo.errorCalls)
						require.WithinDuration(t, started.Add(7*time.Minute), repo.until, 5*time.Second)
					}
					if tc.invalidate {
						require.Equal(t, []int64{account.ID}, invalidator.ids)
					} else {
						require.Empty(t, invalidator.ids)
					}
					require.True(t, svc.isOpenAIAccountRuntimeBlocked(account), "stale scheduler snapshots must be excluded immediately")
					require.True(t, account.IsSchedulable(), "do not mutate the shared account snapshot")
					require.True(t, account.IsExcelBPSEnabled())
					requireNoExcelBPSQuotaWrite(t, &repo.excelBPSQuotaRepo)
					for _, secret := range []string{"test-token", "refresh-secret", "PRIVATE_REQUEST", "file-private"} {
						require.NotContains(t, repo.reason, secret)
						require.NotContains(t, rec.Body.String(), secret)
					}
				})
			}
		}
	}
}
