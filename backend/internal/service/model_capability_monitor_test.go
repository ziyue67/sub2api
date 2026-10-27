//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModelCapabilityCooldownDiagnosis(t *testing.T) {
	for _, tc := range []struct {
		name, reason  string
		expired, peer bool
	}{
		{"model missing", upstreamModelNotFoundReason, false, false},
		{"provider 401 model missing", upstreamModelNotFound401Reason, false, false},
		{"plan gated", upstreamCodexPlanGatedModelReason, false, false},
		{"ordinary rate limit", "upstream_429", false, false},
		{"unknown reason", "", false, false},
		{"expired", upstreamModelNotFoundReason, true, false},
		{"another provider supports it", upstreamModelNotFoundReason, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.2-chat-latest": "gpt-5.2-chat-latest"}}}
			until := time.Now().Add(time.Hour)
			if tc.expired {
				until = time.Now().Add(-time.Hour)
			}
			setAccountModelRateLimitSnapshot(&account, "gpt-5.2", until, tc.reason, time.Now())
			repo := &mockAccountRepoForPlatform{accounts: []Account{account}, accountsByID: map[int64]*Account{}}
			if tc.peer {
				repo.accounts = append(repo.accounts, Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true})
			}
			for i := range repo.accounts {
				repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
			}
			generic := (&GatewayService{accountRepo: repo, cfg: testConfig()}).DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5.2-chat-latest", PlatformOpenAI)
			openai := (&OpenAIGatewayService{accountRepo: repo, cfg: testConfig()}).DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5.2-chat-latest", PlatformOpenAI)
			for _, diag := range []ModelAvailabilityDiagnosis{generic, openai} {
				require.True(t, diag.HasAccountsInPool)
				require.True(t, diag.HasModelSupport, "expiring provider observations cannot erase configured support")
			}
			require.Equal(t, tc.expired, account.IsSchedulableForModel("gpt-5.2"), "outbound model remains blocked until expiry")
			require.True(t, account.IsSchedulableForModel("another-model"), "block must remain scoped to one account/model")
		})
	}
}

func TestOpenAIAdmissionPreservesCapabilityCooldownCause(t *testing.T) {
	for _, reason := range []string{upstreamModelNotFoundReason, upstreamModelNotFound401Reason, upstreamCodexPlanGatedModelReason, "upstream_429", ""} {
		t.Run(reason, func(t *testing.T) {
			account := ticketTestAccount(901)
			setAccountModelRateLimitSnapshot(account, "gpt-5.2", time.Now().Add(time.Hour), reason, time.Now())
			svc := &OpenAIGatewayService{accountRepo: &turnAdmissionRepo{account: account}}
			_, err := svc.admitOpenAITurn(context.Background(), nil, account, "gpt-5.2")
			require.True(t, IsOpenAITurnAdmissionError(err))
			require.Equal(t, reason != "upstream_429" && reason != "", IsOpenAIModelCapabilityRejection(err))
		})
	}
}

func TestAnthropicModelCapabilityCooldownDiagnosis(t *testing.T) {
	for _, reason := range []string{upstreamModelNotFoundReason, "upstream_429", ""} {
		t.Run(reason, func(t *testing.T) {
			account := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-example": "claude-provider-example"}}}
			setAccountModelRateLimitSnapshot(&account, "claude-provider-example", time.Now().Add(time.Hour), reason, time.Now())
			repo := &mockAccountRepoForPlatform{accounts: []Account{account}, accountsByID: map[int64]*Account{1: &account}}
			svc := &GatewayService{accountRepo: repo, cfg: testConfig()}
			diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "claude-example", PlatformAnthropic)
			require.True(t, diag.HasAccountsInPool)
			require.True(t, diag.HasModelSupport, "configured support survives an expiring observation")
			require.False(t, account.IsSchedulableForModel("claude-example"), "diagnosis must not bypass the existing scheduler block")
			setAccountModelRateLimitSnapshot(&account, "claude-provider-example", time.Now().Add(-time.Second), reason, time.Now())
			require.True(t, account.IsSchedulableForModel("claude-example"), "expiry, without clearing history, permits scheduling")
			require.False(t, account.modelCapabilityRejected("another-model"), "a rejection must remain model scoped")
		})
	}
}
