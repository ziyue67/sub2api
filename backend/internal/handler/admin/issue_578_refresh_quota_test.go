//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type issue578RefreshRepo struct {
	service.AccountRepository
	account   *service.Account
	failUsage bool
}

func (r *issue578RefreshRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}
func (r *issue578RefreshRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if _, usage := updates["codex_usage_updated_at"]; usage && r.failUsage {
		return errors.New("synthetic snapshot failure")
	}
	for k, v := range updates {
		r.account.Extra[k] = v
	}
	return nil
}

// Exercise the real handler -> quota service -> scheduler snapshot seam. A
// display cache succeeding must not conceal a failed scheduling-window write.
func TestIssue578RefreshQuotaSynchronizesSchedulingWindows(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "partial persistence reported"}[fail], func(t *testing.T) {
			r := &issue578RefreshRepo{failUsage: fail, account: &service.Account{
				ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "synthetic-oauth", "chatgpt_account_id": "fixture-account", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339), "account_scheduling_threshold": 90},
				Extra:       map[string]any{"openai_excel_bps": true, "codex_5h_used_percent": 89.0},
			}}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				require.Equal(t, http.MethodGet, request.Method, "refresh cannot consume reset cards")
				require.Equal(t, "Bearer synthetic-oauth", request.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/backend-api/wham/usage":
					_, _ = w.Write([]byte(`{"account_id":"fixture-account","rate_limit":{"allowed":true,"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_after_seconds":86400}},"credits":{"has_credits":true,"balance":"20.00"},"rate_limit_reset_credits":{"available_count":1}}`))
				case "/backend-api/wham/rate-limit-reset-credits":
					_, _ = w.Write([]byte(`{"available_count":1}`))
				default:
					t.Errorf("unexpected upstream path %s", request.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			target, err := url.Parse(srv.URL)
			require.NoError(t, err)
			factory := func(string) (*req.Client, error) {
				return req.C().WrapRoundTripFunc(func(rt req.RoundTripper) req.RoundTripFunc {
					return func(request *req.Request) (*req.Response, error) {
						request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
						return rt.RoundTrip(request)
					}
				}), nil
			}
			quota := service.NewOpenAIQuotaService(r, nil, service.NewOpenAITokenProvider(r, nil, nil), factory, nil)
			h := &OpenAIOAuthHandler{quotaService: quota}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.POST("/api/v1/admin/openai/accounts/:id/quota/refresh", h.RefreshQuota)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/openai/accounts/42/quota/refresh", nil))
			require.Equal(t, 200, recorder.Code)
			var envelope struct {
				Data struct {
					UsagePersisted   bool `json:"usage_cache_persisted"`
					CreditsPersisted bool `json:"credits_cache_persisted"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
			require.Equal(t, !fail, envelope.Data.UsagePersisted)
			require.True(t, envelope.Data.CreditsPersisted)
			want := 100.0
			if fail {
				want = 89
			}
			require.Equal(t, want, r.account.Extra["codex_5h_used_percent"])
			require.Equal(t, !fail, service.EvaluateAccountSchedulingThreshold(r.account, nil, time.Now()).ShouldPause)
		})
	}
}
