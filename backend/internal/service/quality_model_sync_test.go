package service

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type qualityModelSyncAccounts struct {
	AccountRepository
	account   *Account
	persisted map[string]any
	err       error
}

func (r *qualityModelSyncAccounts) GetByID(context.Context, int64) (*Account, error) {
	account := *r.account
	account.Extra = maps.Clone(r.persisted)
	return &account, nil
}

func (r *qualityModelSyncAccounts) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.err != nil {
		return r.err
	}
	// Round-trip JSON like the repository, independently of the sync pointer.
	raw, err := json.Marshal(updates)
	if err != nil {
		return err
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	if r.persisted == nil {
		r.persisted = map[string]any{}
	}
	maps.Copy(r.persisted, stored)
	return nil
}

// All discovery and registry requests terminate in this in-memory transport.
// No model execution, database or external network is used.
func qualityModelSyncService(t *testing.T, account *Account, status int, complete bool, ids ...string) (*ScheduledTestService, *AccountTestService) {
	t.Helper()
	entries := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		entry := map[string]any{"id": id}
		if complete {
			entry["context_window"] = 256000
			entry["input_modalities"] = []string{"text"}
			entry["supported_reasoning_levels"] = []string{"high"}
		}
		entries = append(entries, entry)
	}
	body, err := json.Marshal(map[string]any{"data": entries})
	require.NoError(t, err)
	transport := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		if req.URL.Host != "models.example" || req.URL.Path != "/v1/models" {
			return nil, errors.New("offline registry unavailable")
		}
		response := ordinaryModelsUpstreamResponse(string(body))
		response.StatusCode = status
		return response, nil
	}}
	tests := &AccountTestService{
		accountRepo:  &qualityModelSyncAccounts{account: account, persisted: maps.Clone(account.Extra)},
		httpUpstream: transport, cfg: upstreamModelSyncTestConfig(),
		openaiGatewayService: newCodexModelsAPIKeyTestService(transport),
	}
	scheduled := ProvideScheduledTestService(nil, nil, nil, tests)
	return scheduled, tests
}

func TestQualityModelSyncRecoversRejectedModels(t *testing.T) {
	for _, tc := range []struct {
		name, public, target string
		mapping              map[string]any
		complete             bool
	}{
		{"same whitelist", "gpt-6-astra", "gpt-6-astra", map[string]any{"gpt-6-astra": "gpt-6-astra"}, true},
		{"alias", "gpt-6-astra", "upstream-astra", map[string]any{"gpt-6-astra": "upstream-astra"}, true},
		{"wildcard", "gpt-6-astra", "gpt-6-astra", map[string]any{"gpt-*": "gpt-6-astra"}, true},
		{"wildcard target", "gpt-6-astra", "upstream-astra", map[string]any{"gpt-*": "upstream-astra"}, true},
		{"incomplete capabilities", "gpt-6-astra", "gpt-6-astra", map[string]any{"gpt-6-astra": "gpt-6-astra"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
			account.Credentials["model_mapping"] = tc.mapping
			// Legacy persisted rejection, shared by every quality rule.
			account.Extra = map[string]any{
				qualityUnsupportedModelKey(tc.public):      tc.public,
				qualityUnsupportedModelKey("absent-model"): "absent-model",
				"unrelated-setting":                        true,
			}
			scheduled, tests := qualityModelSyncService(t, account, http.StatusOK, tc.complete, tc.target)
			plan := multiModelQualityPlan(t, `["gpt-6-astra"]`)
			plan.AccountID = account.ID
			require.ErrorContains(t, scheduled.validateQualityAccountModels(ctx, plan), tc.public)
			_, err := tests.SyncUpstreamModelCatalog(ctx, account)
			require.NoError(t, err)
			// Re-read persisted state, as the next rule/refresh does. Reloading must
			// not rely solely on mutations to the sync handler's Account pointer.
			reloaded, err := tests.accountRepo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.NoError(t, scheduled.validateQualityAccountModels(ctx, plan))
			require.Equal(t, tc.target, reloaded.GetMappedModel(tc.public))
			require.Equal(t, "absent-model", reloaded.Extra[qualityUnsupportedModelKey("absent-model")])
			require.Equal(t, true, reloaded.Extra["unrelated-setting"])
			catalog, err := scheduled.accountQualityModels(ctx, account.ID)
			require.NoError(t, err)
			if _, exact := tc.mapping[tc.public]; exact {
				require.Contains(t, catalog, tc.public)
			}
			// A live listing permits a new check; an actual execution rejection
			// still suppresses it again, including through configured wildcards.
			require.NoError(t, scheduled.rememberUnsupportedQualityModel(ctx, account.ID, tc.public))
			require.ErrorContains(t, scheduled.validateQualityAccountModels(ctx, plan), tc.public)
		})
	}
}

func TestQualityModelSyncPreservesUnsupportedProtection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		ids       []string
		wantError bool
		target    string
	}{
		{"model absent", http.StatusOK, []string{"other-model"}, false, ""},
		{"catalog failure", http.StatusServiceUnavailable, nil, true, ""},
		{"configured fallback is not live support", http.StatusNotFound, nil, false, ""},
		{"public name listed but execution target absent", http.StatusOK, []string{"gpt-6-astra"}, false, "absent-target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
			account.Credentials["model_mapping"] = map[string]any{"gpt-*": "*", "configured-model": "configured-model"}
			if tc.target != "" {
				mapping, ok := account.Credentials["model_mapping"].(map[string]any)
				require.True(t, ok)
				mapping["gpt-6-astra"] = tc.target
			}
			account.Extra = map[string]any{qualityUnsupportedModelKey("gpt-6-astra"): "gpt-6-astra"}
			scheduled, tests := qualityModelSyncService(t, account, tc.status, true, tc.ids...)
			_, err := tests.SyncUpstreamModelCatalog(ctx, account)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			plan := multiModelQualityPlan(t, `["gpt-6-astra"]`)
			plan.AccountID = account.ID
			require.ErrorContains(t, scheduled.validateQualityAccountModels(ctx, plan), "不支持检测模型")
			require.Equal(t, "gpt-6-astra", account.Extra[qualityUnsupportedModelKey("gpt-6-astra")])
		})
	}
}

func TestQualityModelSyncDiscoveryFailureDoesNotRememberRejection(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	scheduled, _ := qualityModelSyncService(t, account, http.StatusServiceUnavailable, false)
	plan := multiModelQualityPlan(t, `["gpt-6-astra"]`)
	plan.AccountID = account.ID
	err := scheduled.validateQualityAccountModels(context.Background(), plan)
	require.ErrorContains(t, err, "无法读取账号")
	require.NotContains(t, err.Error(), "不支持检测模型")
	require.Empty(t, account.Extra)
}

func TestQualityModelSyncResetPersistenceFailureIsReported(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	key := qualityUnsupportedModelKey("gpt-6-astra")
	account.Extra = map[string]any{key: "gpt-6-astra"}
	_, tests := qualityModelSyncService(t, account, http.StatusOK, false, "gpt-6-astra")
	repo, ok := tests.accountRepo.(*qualityModelSyncAccounts)
	require.True(t, ok)
	repo.err = errors.New("offline persistence failure")
	_, err := tests.SyncUpstreamModelCatalog(context.Background(), account)
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, UpstreamModelSyncErrorInternal, syncErr.Kind)
	require.Equal(t, "gpt-6-astra", account.Extra[key])
}
