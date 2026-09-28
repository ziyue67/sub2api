package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGetAvailableModels_RestrictedDefaultCatalogWithMappedAccount(t *testing.T) {
	groupID := int64(10)
	for _, passthrough := range []bool{true, false} {
		for _, tc := range []struct {
			name                     string
			allowed, present, absent []string
		}{
			{"gpt wildcard", []string{"gpt-*"}, []string{"gpt-5.5", "gpt-image-1"}, []string{"codex-auto-review"}},
			{"narrow wildcard", []string{"gpt-5.4*"}, []string{"gpt-5.4", "gpt-5.4-mini"}, []string{"gpt-5.5", "gpt-image-1", "codex-auto-review"}},
			{"exact catalog model", []string{"gpt-5.5"}, []string{"gpt-5.5"}, []string{"gpt-5.4", "codex-auto-review"}},
			{"custom model", []string{"allowed-model"}, []string{"allowed-model"}, []string{"gpt-5.5", "codex-auto-review"}},
			{"nonmatching wildcard", []string{"unknown-*"}, nil, []string{"gpt-5.5", "codex-auto-review"}},
		} {
			name := tc.name + "/unmapped"
			if passthrough {
				name = tc.name + "/passthrough"
			}
			t.Run(name, func(t *testing.T) {
				account := Account{ID: 1, Platform: PlatformOpenAI, Extra: map[string]any{"openai_passthrough": passthrough}, AccountGroups: []AccountGroup{{GroupID: groupID, AllowedModels: tc.allowed}}}
				if passthrough {
					account.Credentials = map[string]any{"model_mapping": map[string]any{"stale-model": "stale-upstream"}}
				}
				repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account, {ID: 2, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-upstream"}}}}}}
				svc := &GatewayService{accountRepo: repo}
				models := svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI)
				require.Contains(t, models, "configured-model")
				require.NotContains(t, models, "stale-model")
				for _, model := range tc.present {
					require.True(t, account.IsModelAllowedInGroup(&groupID, model))
					require.Contains(t, models, model, "allowed models must remain discoverable")
				}
				for _, model := range tc.absent {
					require.NotContains(t, models, model)
				}
				for _, model := range models {
					if model != "configured-model" {
						require.True(t, account.IsModelAllowedInGroup(&groupID, model), "restricted account leaked %s", model)
					}
				}
			})
		}
	}
}
