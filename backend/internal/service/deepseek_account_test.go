package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAccountCredentialsForPlatformDeepSeek(t *testing.T) {
	input := map[string]any{
		"api_key": "  custom-key_123  ",
		"headers": map[string]any{"x-test": "value"},
	}

	got, err := normalizeAccountCredentialsForPlatform(PlatformDeepSeek, AccountTypeAPIKey, input)
	require.NoError(t, err)
	require.Equal(t, "custom-key_123", got["api_key"])
	require.Equal(t, DefaultDeepSeekBaseURL, got["base_url"])
	require.NotContains(t, input, "base_url", "normalization must not mutate the request map")

	custom, err := normalizeAccountCredentialsForPlatform(PlatformDeepSeek, AccountTypeAPIKey, map[string]any{
		"api_key":  "no-prefix-required",
		"base_url": " http://relay.example.test/deepseek/// ",
	})
	require.NoError(t, err)
	require.Equal(t, "http://relay.example.test/deepseek", custom["base_url"])

	officialV1, err := normalizeAccountCredentialsForPlatform(PlatformDeepSeek, AccountTypeAPIKey, map[string]any{
		"api_key":  "official-alias",
		"base_url": " https://api.deepseek.com/v1/// ",
	})
	require.NoError(t, err)
	require.Equal(t, DefaultDeepSeekBaseURL, officialV1["base_url"], "the official /v1 alias must canonicalize to the shared native API root")
}

func TestNormalizeAccountCredentialsForPlatformDeepSeekRejectsUnsupportedType(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeUpstream, AccountTypeServiceAccount, AccountTypeBedrock} {
		t.Run(accountType, func(t *testing.T) {
			_, err := normalizeAccountCredentialsForPlatform(PlatformDeepSeek, accountType, map[string]any{"api_key": "valid-key"})
			require.Error(t, err)
			require.Equal(t, "DEEPSEEK_ACCOUNT_TYPE_INVALID", infraerrors.Reason(err))
		})
	}
}

func TestNormalizeAccountCredentialsForPlatformDeepSeekRejectsInvalidAPIKey(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "missing", value: nil},
		{name: "empty", value: "  "},
		{name: "non string", value: 123},
		{name: "embedded space", value: "key with-space"},
		{name: "newline", value: "key\nvalue"},
		{name: "non ascii", value: "key-\u5bc6\u94a5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credentials := map[string]any{}
			if tt.name != "missing" {
				credentials["api_key"] = tt.value
			}
			_, err := normalizeAccountCredentialsForPlatform(PlatformDeepSeek, AccountTypeAPIKey, credentials)
			require.Error(t, err)
			require.Contains(t, []string{"DEEPSEEK_API_KEY_REQUIRED", "DEEPSEEK_API_KEY_INVALID"}, infraerrors.Reason(err))
		})
	}
}

func TestNormalizeAccountCredentialsForPlatformDeepSeekRejectsInvalidBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "non string", value: 123},
		{name: "relative", value: "/v1"},
		{name: "unsupported scheme", value: "ftp://api.deepseek.com"},
		{name: "userinfo", value: "https://user:pass@api.deepseek.com"},
		{name: "query", value: "https://api.deepseek.com?key=secret"},
		{name: "fragment", value: "https://api.deepseek.com#fragment"},
		{name: "custom v1 suffix", value: "https://relay.example.test/v1"},
		{name: "custom prefixed v1 suffix", value: "https://relay.example.test/deepseek/v1/"},
		{name: "models endpoint", value: "https://api.deepseek.com/models"},
		{name: "versioned models endpoint", value: "https://api.deepseek.com/v1/models/"},
		{name: "mixed case prefixed models endpoint", value: "https://relay.example.test/deepseek/V1/MoDeLs///"},
		{name: "chat completions endpoint", value: "https://api.deepseek.com/chat/completions"},
		{name: "versioned chat completions endpoint", value: "https://api.deepseek.com/v1/chat/completions/"},
		{name: "responses endpoint", value: "https://api.deepseek.com/responses"},
		{name: "versioned responses endpoint", value: "https://api.deepseek.com/v1/responses/"},
		{name: "anthropic messages endpoint", value: "https://api.deepseek.com/anthropic/v1/messages"},
		{name: "versioned anthropic messages endpoint", value: "https://api.deepseek.com/v1/anthropic/v1/messages/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeAccountCredentialsForPlatform(PlatformDeepSeek, AccountTypeAPIKey, map[string]any{
				"api_key":  "valid-key",
				"base_url": tt.value,
			})
			require.Error(t, err)
			require.Equal(t, "DEEPSEEK_BASE_URL_INVALID", infraerrors.Reason(err))
		})
	}
}

func TestDeepSeekAccountHelpersAndEndpointCapabilities(t *testing.T) {
	account := &Account{
		Platform: PlatformDeepSeek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "  key-123  ",
			"base_url": "https://relay.example.test/deepseek/",
		},
	}
	require.True(t, account.IsDeepSeek())
	require.True(t, account.IsDeepSeekAPIKey())
	require.True(t, account.IsOpenAICompatible())
	require.Equal(t, "key-123", account.GetDeepSeekAPIKey())
	require.Equal(t, "https://relay.example.test/deepseek", account.GetDeepSeekBaseURL())
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))

	account.Credentials = map[string]any{"api_key": "key-123"}
	require.Equal(t, DefaultDeepSeekBaseURL, account.GetDeepSeekBaseURL())
}

func TestAdminServiceCreateAccountNormalizesDeepSeekIdentity(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{}
	created, err := (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "deepseek",
		Platform:             "  DeepSeek  ",
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "  arbitrary-key  "},
		SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Equal(t, PlatformDeepSeek, created.Platform)
	require.Equal(t, "arbitrary-key", created.Credentials["api_key"])
	require.Equal(t, DefaultDeepSeekBaseURL, created.Credentials["base_url"])

	_, err = (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "invalid",
		Platform:             " DEEPSEEK ",
		Type:                 AccountTypeOAuth,
		Credentials:          map[string]any{"api_key": "key"},
		SkipDefaultGroupBind: true,
	})
	require.Equal(t, "DEEPSEEK_ACCOUNT_TYPE_INVALID", infraerrors.Reason(err))

	_, err = (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "unknown",
		Platform:             "openrouter",
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "key"},
		SkipDefaultGroupBind: true,
	})
	require.Equal(t, "ACCOUNT_PLATFORM_INVALID", infraerrors.Reason(err))
}

func TestAdminServiceUpdateAccountPreservesAndValidatesDeepSeekIdentity(t *testing.T) {
	const accountID int64 = 991
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		accountID: {
			ID:       accountID,
			Name:     "before",
			Platform: PlatformDeepSeek,
			Type:     AccountTypeAPIKey,
			Status:   StatusActive,
			Credentials: map[string]any{
				"api_key":  "original-key",
				"base_url": "https://relay.example.test/deepseek/",
			},
		},
	}}
	svc := &adminServiceImpl{accountRepo: repo}

	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Name:        "after",
		Credentials: map[string]any{"base_url": "https://api.deepseek.com///"},
	})
	require.NoError(t, err)
	require.Equal(t, "original-key", updated.Credentials["api_key"])
	require.Equal(t, DefaultDeepSeekBaseURL, updated.Credentials["base_url"])

	_, err = svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{Type: AccountTypeOAuth})
	require.Equal(t, "DEEPSEEK_ACCOUNT_TYPE_INVALID", infraerrors.Reason(err))

	_, err = svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{"api_key": " "},
	})
	require.Equal(t, "DEEPSEEK_API_KEY_REQUIRED", infraerrors.Reason(err))
}

func TestAccountServiceCreateAndUpdateUseDeepSeekIdentityValidation(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{}
	svc := NewAccountService(repo, nil)
	created, err := svc.Create(context.Background(), CreateAccountRequest{
		Name:        "deepseek-secondary-service",
		Platform:    " DEEPSEEK ",
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "  secondary-key  "},
	})
	require.NoError(t, err)
	require.Equal(t, PlatformDeepSeek, created.Platform)
	require.Equal(t, "secondary-key", created.Credentials["api_key"])
	require.Equal(t, DefaultDeepSeekBaseURL, created.Credentials["base_url"])

	baseURLOnly := map[string]any{"base_url": "https://relay.example.test/deepseek/"}
	updated, err := svc.Update(context.Background(), created.ID, UpdateAccountRequest{Credentials: &baseURLOnly})
	require.NoError(t, err)
	require.Equal(t, "secondary-key", updated.Credentials["api_key"])
	require.Equal(t, "https://relay.example.test/deepseek", updated.Credentials["base_url"])

	invalid := map[string]any{"api_key": "invalid key"}
	_, err = svc.Update(context.Background(), created.ID, UpdateAccountRequest{Credentials: &invalid})
	require.Equal(t, "DEEPSEEK_API_KEY_INVALID", infraerrors.Reason(err))
}

func TestLegacyUnsupportedPlatformUpdatePathsRemainCompatible(t *testing.T) {
	const accountID int64 = 992
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		accountID: {
			ID:          accountID,
			Name:        "legacy-kiro",
			Platform:    PlatformKiro,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Credentials: map[string]any{"api_key": "legacy key with spaces"},
		},
	}}
	svc := &adminServiceImpl{accountRepo: repo}

	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{Name: "legacy-kiro-updated"})
	require.NoError(t, err)
	require.Equal(t, PlatformKiro, updated.Platform)
	require.Equal(t, "legacy-kiro-updated", updated.Name)

	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs:  []int64{accountID},
		Credentials: map[string]any{"api_key": "another legacy key"},
	})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)

	_, err = svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "new-kiro",
		Platform:             PlatformKiro,
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "key"},
		SkipDefaultGroupBind: true,
	})
	require.Equal(t, "ACCOUNT_PLATFORM_INVALID", infraerrors.Reason(err))
}

func TestDeepSeekDefaultModelIDsReturnsCopy(t *testing.T) {
	first := DeepSeekDefaultModelIDs()
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, first)
	first[0] = "mutated"
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, DeepSeekDefaultModelIDs())
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, defaultModelsListCandidateIDs(PlatformDeepSeek))
	require.Subset(t, compositeDefaultModelsListCandidateIDs(), DeepSeekDefaultModelIDs())
}
