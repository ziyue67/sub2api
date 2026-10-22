//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestHandle429_OpenAIAPIKeyIgnoresRelayedQuotaWindows(t *testing.T) {
	for _, source := range []string{"body_reset_at", "body_reset_seconds", "codex_headers", "anthropic_headers", "opencode_body", "ordinary_429"} {
		t.Run(source, func(t *testing.T) {
			repo := &openAI429SnapshotRepo{}
			svc := NewRateLimitService(repo, nil, nil, nil, nil)
			blocker := &runtimeBlockRecorder{}
			svc.SetAccountRuntimeBlocker(blocker)
			account := &Account{ID: 125, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			headers := http.Header{}
			body := []byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`)
			switch source {
			case "body_reset_at":
				body = []byte(`{"error":{"type":"usage_limit_reached","plan_type":"pro","resets_at":4102444800}}`)
			case "body_reset_seconds":
				body = []byte(`{"error":{"type":"rate_limit_exceeded","resets_in_seconds":604800}}`)
			case "opencode_body":
				body = []byte(`{"error":{"type":"GoUsageLimitError","message":"5-hour usage limit reached. Resets in 4hr 59min."}}`)
			case "codex_headers":
				headers.Set("x-codex-primary-used-percent", "100")
				headers.Set("x-codex-primary-reset-after-seconds", "604800")
				headers.Set("x-codex-primary-window-minutes", "10080")
			case "anthropic_headers":
				headers.Set("anthropic-ratelimit-unified-reset", "4102444800")
			}

			before := time.Now()
			svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, headers, body)
			after := time.Now()

			cooldown := time.Duration(defaultRateLimit429CooldownSeconds) * time.Second
			require.Equal(t, account.ID, repo.rateLimitedID)
			require.False(t, repo.rateLimitResetAt.Before(before.Add(cooldown)))
			require.False(t, repo.rateLimitResetAt.After(after.Add(cooldown)))
			require.Empty(t, repo.updatedExtra, "relayed Codex quota must not become the key's snapshot")
			require.Empty(t, repo.bulkUpdatedIDs)
			require.Equal(t, []*Account{account}, blocker.accounts)
			require.Equal(t, []string{"429_fallback"}, blocker.reasons)
			require.Equal(t, repo.rateLimitResetAt, blocker.until[0])
		})
	}
}

func TestHandle429_OpenAIAPIKeyQuotaRespectsFallbackSettings(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			repo := &openAI429SnapshotRepo{}
			settingsRepo := newMockSettingRepo()
			data, err := json.Marshal(RateLimit429CooldownSettings{Enabled: enabled, CooldownSeconds: 12})
			require.NoError(t, err)
			settingsRepo.data[SettingKeyRateLimit429CooldownSettings] = string(data)
			svc := NewRateLimitService(repo, nil, nil, nil, nil)
			svc.SetSettingService(NewSettingService(settingsRepo, &config.Config{}))
			blocker := &runtimeBlockRecorder{}
			svc.SetAccountRuntimeBlocker(blocker)
			account := &Account{ID: 126, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			body := []byte(`{"error":{"type":"usage_limit_reached","plan_type":"pro","resets_at":4102444800}}`)

			before := time.Now()
			svc.handle429(context.Background(), account, http.Header{}, body)
			after := time.Now()

			if enabled {
				require.Equal(t, account.ID, repo.rateLimitedID)
				require.False(t, repo.rateLimitResetAt.Before(before.Add(12*time.Second)))
				require.False(t, repo.rateLimitResetAt.After(after.Add(12*time.Second)))
				require.Len(t, blocker.accounts, 1)
			} else {
				require.Zero(t, repo.rateLimitedID)
				require.Empty(t, blocker.accounts)
			}
			require.Empty(t, repo.updatedExtra)
		})
	}
}

func TestAccountTestService_OpenAIAPIKey429HeadersDoNotBlockScheduling(t *testing.T) {
	repo := &openAIAccountTestRepo{}
	svc := &AccountTestService{accountRepo: repo}
	account := &Account{ID: 127, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusError, ErrorMessage: "existing error"}
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")

	svc.reconcileOpenAI429State(context.Background(), account, headers, nil)

	require.Zero(t, repo.rateLimitedID)
	require.Zero(t, repo.clearedErrorID)
	require.Nil(t, account.RateLimitResetAt)
	require.Equal(t, StatusError, account.Status)
	require.Equal(t, "existing error", account.ErrorMessage)
}
