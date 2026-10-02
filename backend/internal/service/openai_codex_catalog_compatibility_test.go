package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupCodexCatalogMixedAstraServiceTiers(t *testing.T) {
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://proxy.example", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}},
	}
	svc := &OpenAIGatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: accounts}}}
	manifest, configured, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 1, Platform: PlatformOpenAI}, "")
	require.NoError(t, err)
	require.True(t, configured)
	models := decodeCodexManifestModels(t, manifest.Body)
	require.Len(t, models, 1)
	require.Equal(t, []any{}, models[0]["service_tiers"], "mixed capabilities must remain a non-nullable array")
}

func TestGroupCodexCatalogProxySolUsesFullResponses(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "openai/gpt-6.1-sol", "gpt-6.1-sol-max"} {
		for _, snapshot := range []string{"", "true", "false"} {
			t.Run(model+"/snapshot="+snapshot, func(t *testing.T) {
				account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
					"base_url": "https://proxy.example", "model_mapping": map[string]any{"public-sol": model},
				}}
				if snapshot != "" {
					account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
						model: {CodexToolCapabilities: map[string]json.RawMessage{"use_responses_lite": json.RawMessage(snapshot)}},
					}})
				}
				svc := &OpenAIGatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: {account}}}}
				manifest, configured, err := svc.BuildGroupConfiguredCodexModelsManifest(context.Background(), &Group{ID: 1, Platform: PlatformOpenAI}, "")
				require.NoError(t, err)
				require.True(t, configured)
				models := decodeCodexManifestModels(t, manifest.Body)
				require.Len(t, models, 1)
				require.Equal(t, false, models[0]["use_responses_lite"], "API-key providers must advertise full Responses")

				body, err := adjustAPIKeyCodexModelsManifest([]byte(`{"models":[{"slug":"public-sol","use_responses_lite":true}]}`), &account)
				require.NoError(t, err)
				require.Equal(t, false, decodeCodexManifestModels(t, body)[0]["use_responses_lite"])
			})
		}
	}
}

func TestGroupCodexCatalogServiceTierDeclarations(t *testing.T) {
	const priority = `[{"id":"priority","name":"Fast","description":"Priority processing"}]`
	const ultrafast = `[{"id":"ultrafast","name":"Ultrafast","description":"Low latency"}]`
	for _, tc := range []struct {
		name         string
		declarations []string
		want         string
	}{
		{"shared", []string{priority, priority}, priority},
		{"conflicting", []string{priority, ultrafast}, "[]"},
		{"missing peer", []string{priority, ""}, "[]"},
		{"explicit null", []string{"null"}, "[]"},
		{"explicit empty", []string{"[]"}, "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := make([]Account, 0, len(tc.declarations))
			for i, raw := range tc.declarations {
				account := Account{ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
					"base_url": "https://proxy.example", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"},
				}}
				if raw != "" {
					account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
						"gpt-6-astra": {CodexToolCapabilities: map[string]json.RawMessage{"service_tiers": json.RawMessage(raw)}},
					}})
				}
				accounts = append(accounts, account)
			}
			body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"gpt-6-astra"}, accounts, nil, nil, true)
			require.NoError(t, err)
			model := decodeCodexManifestModels(t, body)[0]
			tiers, err := json.Marshal(model["service_tiers"])
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(tiers))
		})
	}
}

func TestGroupCodexCatalogOAuthSolRetainsResponsesLite(t *testing.T) {
	account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"base_url": "https://chatgpt.com", "model_mapping": map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"},
	}}
	body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"gpt-6.1-sol"}, []Account{account}, nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, true, decodeCodexManifestModels(t, body)[0]["use_responses_lite"])
}
