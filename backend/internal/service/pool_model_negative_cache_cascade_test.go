//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The fastpath calls this writer before the generic pool guard. Exercise all
// three entry points so moving the guard at just one caller cannot hide a leak.
func TestPoolModelNegativeCacheWriterRespectsPoolDefaultAndOverrides(t *testing.T) {
	for _, status := range []int{404, 401} {
		for _, policy := range []string{"pool-default", "custom-include", "custom-exclude", "temp-rule", "nonpool"} {
			for _, entry := range []string{"writer", "shared-policy", "http-fastpath"} {
				t.Run(fmt.Sprintf("status=%d/policy=%s/entry=%s", status, policy, entry), func(t *testing.T) {
					repo := &errorPolicyRepoStub{}
					rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
					gateway := &OpenAIGatewayService{rateLimitService: rateLimits}
					account := &Account{ID: 301, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
						Status: StatusActive, Schedulable: true, Credentials: map[string]any{"pool_mode": policy != "nonpool"}}
					if policy == "custom-include" || policy == "custom-exclude" {
						code := status
						if policy == "custom-exclude" {
							code = 503
						}
						account.Credentials["custom_error_codes_enabled"] = true
						account.Credentials["custom_error_codes"] = []any{float64(code)}
					}
					if policy == "temp-rule" {
						account.Credentials["temp_unschedulable_enabled"] = true
						account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
							"error_code": float64(status), "keywords": []any{"unknown model"},
							"duration_minutes": float64(7), "scope": "model",
						}}
					}
					body := []byte(`{"error":{"code":"model_not_found","message":"unknown model provider-sol"}}`)
					switch entry {
					case "writer":
						rateLimits.HandleUpstreamModelNotFound(context.Background(), account, "provider-sol", status, body)
					case "shared-policy":
						rateLimits.HandleUpstreamError(context.Background(), account, status, http.Header{}, body, "provider-sol")
					case "http-fastpath":
						gateway.handleOpenAIAccountUpstreamError(context.Background(), account, status, http.Header{}, body, "provider-sol")
					}
					wantWrite := policy == "custom-include" || policy == "nonpool" || (policy == "temp-rule" && entry != "writer")
					if !wantWrite {
						require.Empty(t, repo.modelRateLimitCalls, "default pool must not cache a relay member's observation")
					} else {
						require.Len(t, repo.modelRateLimitCalls, 1)
						call := repo.modelRateLimitCalls[0]
						require.Equal(t, account.ID, call.accountID)
						require.Equal(t, "provider-sol", call.scope)
						cooldown := 30 * time.Minute
						if policy == "temp-rule" {
							cooldown = 7 * time.Minute
							require.Contains(t, call.reason, `"scope":"model"`)
						} else if status == 401 {
							require.Equal(t, upstreamModelNotFound401Reason, call.reason)
						} else {
							require.Equal(t, upstreamModelNotFoundReason, call.reason)
						}
						require.WithinDuration(t, time.Now().Add(cooldown), call.resetAt, 2*time.Second)
					}
					require.Zero(t, repo.tempCalls, "must not widen a model observation into an account ban")
					require.Zero(t, repo.setErrCalls)
				})
			}
		}
	}
}

func TestPoolModelNegativeCacheDiagnosisPreservesConfiguredSupportAndSchedulingBlock(t *testing.T) {
	for _, reason := range []string{upstreamModelNotFoundReason, upstreamModelNotFound401Reason, upstreamCodexPlanGatedModelReason} {
		t.Run(reason, func(t *testing.T) {
			account := Account{ID: 301, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"pool_mode": true, "model_mapping": map[string]any{"public-sol": "provider-sol", "other-model": "other-model"}}}
			setAccountModelRateLimitSnapshot(&account, "provider-sol", time.Now().Add(30*time.Second), reason, time.Now())
			repo := &mockAccountRepoForPlatform{accounts: []Account{account}, accountsByID: map[int64]*Account{301: &account}}
			for _, diag := range []ModelAvailabilityDiagnosis{
				(&GatewayService{accountRepo: repo, cfg: testConfig()}).DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "public-sol", PlatformOpenAI),
				(&OpenAIGatewayService{accountRepo: repo, cfg: testConfig()}).DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "public-sol", PlatformOpenAI),
			} {
				require.True(t, diag.Complete)
				require.True(t, diag.HasModelSupport, "expiring observation is not configured lack of support")
				require.Equal(t, 1, diag.ModelSupportedAccounts)
			}
			require.False(t, account.IsSchedulableForModel("public-sol"), "the active historical record still blocks the mapped model")
			require.True(t, account.IsSchedulableForModel("other-model"))
			setAccountModelRateLimitSnapshot(&account, "provider-sol", time.Now().Add(-time.Second), reason, time.Now())
			require.True(t, account.IsSchedulableForModel("public-sol"), "expiry restores scheduling without removing the record")
		})
	}
}
