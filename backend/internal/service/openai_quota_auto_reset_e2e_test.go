package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Exercise saved configuration, scheduling, live quota reads, targeted credit
// consumption and post-reset snapshots together. Only upstream HTTP, account
// persistence, token storage, idempotency storage and recovery are local fixtures.
func TestOpenAIQuotaAutoResetDisabledWindowHTTPFlow(t *testing.T) {
	tests := []struct {
		name                     string
		threshold5h, threshold7d float64
		used5h, used7d           float64
		pause5h, pause7d         float64
		wantReset                bool
		wantPause                bool
		wantReason               string
		wantWindow               string
	}{
		{name: "ignore exhausted 5h below weekly threshold", threshold7d: 0.9, used5h: 100, used7d: 89.9},
		{name: "ignore overused 5h below weekly threshold", threshold7d: 0.9, used5h: 150, used7d: 20},
		{name: "weekly threshold consumes with exhausted 5h", threshold7d: 0.9, used5h: 100, used7d: 90, wantReset: true, wantPause: true, wantReason: "quota_auto_reset_pending_7d", wantWindow: "7d"},
		{name: "weekly threshold consumes with unused 5h", threshold7d: 0.9, used7d: 90, wantReset: true, wantPause: true, wantReason: "quota_auto_reset_pending_7d", wantWindow: "7d"},
		{name: "ignore exhausted 7d below 5h threshold", threshold5h: 0.8, used5h: 79.9, used7d: 100},
		{name: "5h threshold consumes while ignoring 7d", threshold5h: 0.8, used5h: 80, used7d: 100, wantReset: true, wantPause: true, wantReason: "quota_auto_reset_pending_5h", wantWindow: "5h"},
		{name: "both ignored never consume", used5h: 100, used7d: 100},
		{name: "ignored 5h retains normal pause despite available credits", threshold7d: 0.9, used5h: 100, used7d: 85, pause5h: 0.8, pause7d: 0.8, wantPause: true, wantWindow: "5h"},
		{name: "ignored 7d retains normal pause despite available credits", threshold5h: 0.9, used5h: 85, used7d: 100, pause5h: 0.8, pause7d: 0.8, wantPause: true, wantWindow: "7d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			extra, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{
				OpenAIAutoResetCreditEnabledExtraKey:     true,
				OpenAIAutoResetCredit5hThresholdExtraKey: tt.threshold5h,
				OpenAIAutoResetCredit7dThresholdExtraKey: tt.threshold7d,
			})
			require.NoError(t, err)
			extra["auto_pause_5h_disabled"] = tt.pause5h == 0
			extra["auto_pause_7d_disabled"] = tt.pause7d == 0
			extra["auto_pause_5h_threshold"] = tt.pause5h
			extra["auto_pause_7d_threshold"] = tt.pause7d
			extra["codex_5h_used_percent"] = tt.used5h
			extra["codex_7d_used_percent"] = tt.used7d
			extra["codex_5h_reset_at"] = now.Add(time.Hour).Format(time.RFC3339)
			extra["codex_7d_reset_at"] = now.Add(24 * time.Hour).Format(time.RFC3339)
			extra["codex_usage_updated_at"] = now.Format(time.RFC3339)
			extra[OpenAIAutoResetCreditStateExtraKey] = OpenAIAutoResetCreditState{
				Status: OpenAIAutoResetStatusAvailable, AvailableCount: 2, CheckedAt: now.Format(time.RFC3339),
			}
			// Persisted extra is decoded from JSON before either consumer sees it.
			encoded, err := json.Marshal(extra)
			require.NoError(t, err)
			var savedExtra map[string]any
			require.NoError(t, json.Unmarshal(encoded, &savedExtra))
			account := &Account{
				ID: 701, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"chatgpt_account_id": "org-auto-reset-test"}, Extra: savedExtra,
			}
			repo := &autoResetTestAccountRepo{account: account}
			paused, decision := shouldAutoPauseOpenAIAccountByQuota(ctx, account)
			require.Equal(t, tt.wantPause, paused)
			require.Equal(t, tt.wantReason, decision.reason)
			require.Equal(t, tt.wantWindow, decision.window)

			var consumeCalls, usageCalls atomic.Int32
			bodies := make(chan map[string]string, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /backend-api/wham/usage":
					usageCalls.Add(1)
					used5h, used7d := tt.used5h, tt.used7d
					if consumeCalls.Load() > 0 {
						used5h, used7d = 0, 0
					}
					_ = json.NewEncoder(w).Encode(OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{
						PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: used5h, LimitWindowSeconds: 5 * 60 * 60, ResetAfterSeconds: 3600, ResetAt: now.Add(time.Hour).Unix()},
						SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: used7d, LimitWindowSeconds: 7 * 24 * 60 * 60, ResetAfterSeconds: 86400, ResetAt: now.Add(24 * time.Hour).Unix()},
					}})
				case "GET /backend-api/wham/rate-limit-reset-credits":
					credits := []map[string]any{
						{"id": "later-credit", "status": "available", "reset_type": "codex_rate_limits", "expires_at": now.Add(72 * time.Hour).Format(time.RFC3339)},
						{"id": "earlier-credit", "status": "available", "reset_type": "codex_rate_limits", "expires_at": now.Add(48 * time.Hour).Format(time.RFC3339)},
					}
					if consumeCalls.Load() > 0 {
						credits = credits[:1]
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"available_count": len(credits), "credits": credits})
				case "POST /backend-api/wham/rate-limit-reset-credits/consume":
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					bodies <- body
					consumeCalls.Add(1)
					_ = json.NewEncoder(w).Encode(OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2})
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(upstream.Close)
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "fake-token"}}
			quota := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(upstream), nil)
			idempotencyConfig := DefaultIdempotencyConfig()
			idempotencyConfig.ObserveOnly = false
			svc := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, NewIdempotencyCoordinator(newInMemoryIdempotencyRepo(), idempotencyConfig), nil, nil, nil)
			t.Cleanup(svc.Stop)
			// Force a live query even when the cached window does not trigger a reset.
			require.NoError(t, repo.UpdateExtra(ctx, account.ID, map[string]any{"codex_usage_updated_at": now.Add(-time.Hour).Format(time.RFC3339)}))
			require.NoError(t, svc.evaluateAccount(ctx, account.ID))
			require.Positive(t, usageCalls.Load())
			saved, err := repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, tt.threshold5h, saved.Extra[OpenAIAutoResetCredit5hThresholdExtraKey])
			require.Equal(t, tt.threshold7d, saved.Extra[OpenAIAutoResetCredit7dThresholdExtraKey])
			state := openAIAutoResetStateFromExtra(saved.Extra)
			require.NotNil(t, state)
			if tt.wantReset {
				require.Equal(t, int32(1), consumeCalls.Load())
				body := <-bodies
				require.Equal(t, "earlier-credit", body["credit_id"])
				require.NotEmpty(t, body["redeem_request_id"])
				require.Equal(t, OpenAIAutoResetStatusSuccess, state.Status)
				require.Equal(t, tt.wantWindow, state.TriggerWindow)
				require.Equal(t, 1, state.AvailableCount)
				paused, _ = shouldAutoPauseOpenAIAccountByQuota(ctx, saved)
				require.False(t, paused, "post-reset quota returns the account to scheduling")
			} else {
				require.Zero(t, consumeCalls.Load())
				require.Equal(t, OpenAIAutoResetStatusAvailable, state.Status)
				require.Equal(t, 2, state.AvailableCount)
			}
			// A fresh follow-up evaluation must not consume another credit.
			consumed := consumeCalls.Load()
			require.NoError(t, svc.evaluateAccount(ctx, account.ID))
			require.Equal(t, consumed, consumeCalls.Load())
		})
	}
}
