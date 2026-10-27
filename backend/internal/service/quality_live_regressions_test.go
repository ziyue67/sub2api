package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type qualityLivePlanRepo struct{ *templatePlanRepo }

func (r *qualityLivePlanRepo) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	saved, _ := r.add(plan)
	return saved, nil
}

// Exercise the production CreatePlan -> account policy -> discovery seam, not
// only catalog matching. Fixtures never use real credentials or paid inference.
func TestQualityLiveManualModelMissingFromManifest(t *testing.T) {
	for _, mapping := range []map[string]any{nil, {"gpt-6.1-sol": "upstream-sol"}, {"gpt-*": "*"}} {
		t.Run(fmt.Sprint(mapping), func(t *testing.T) {
			accounts := &qualitySupportAccounts{account: &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"model_mapping": mapping}}}
			plans := &qualityLivePlanRepo{newTemplatePlanRepo()}
			svc := NewScheduledTestService(plans, &pelicanResults{})
			svc.accountTests = &AccountTestService{accountRepo: accounts}
			svc.qualityModels = func(context.Context, int64) ([]string, error) {
				if len(mapping) > 0 {
					t.Fatal("configured manual test must not require manifest availability")
				}
				return []string{"gpt-6-sol"}, nil
			}
			plan := multiModelQualityPlan(t, `["gpt-6.1-sol"]`)
			_, err := svc.CreatePlan(context.Background(), plan)
			require.NoError(t, err)
			require.Len(t, plans.plans, 1)
		})
	}
}

func TestQualityLiveUnknownProbeCatalogIsNotUnsupported(t *testing.T) {
	accounts := &qualitySupportAccounts{account: &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}
	svc := NewScheduledTestService(nil, nil)
	svc.accountTests = &AccountTestService{accountRepo: accounts}
	svc.qualityModels = func(context.Context, int64) ([]string, error) { return []string{"gpt-6-sol"}, nil }
	plan := multiModelQualityPlan(t, `["gpt-6.1-sol"]`)
	plan.PelicanConfig.QuestionKind = "state_probe"
	_, err := svc.CreatePlan(context.Background(), plan)
	require.Equal(t, "QUALITY_MODEL_CATALOG_UNKNOWN", infraerrors.Reason(err))
	require.NotContains(t, err.Error(), "不支持检测模型")
}

func TestQualityLiveManualSelectionKeepsPolicyAndObservedRejection(t *testing.T) {
	for _, kind := range []string{"allowlist", "observed", "alias-target"} {
		t.Run(kind, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}, Credentials: map[string]any{}}
			switch kind {
			case "allowlist":
				account.Credentials["model_mapping"] = map[string]any{"other": "other"}
			case "observed":
				account.Extra[qualityUnsupportedModelKey("gpt-6.1-sol")] = "gpt-6.1-sol"
			case "alias-target":
				account.Credentials["model_mapping"] = map[string]any{"gpt-6.1-sol": "target"}
				account.Extra[qualityUnsupportedModelKey("target")] = "target"
			}
			svc := NewScheduledTestService(&qualityLivePlanRepo{newTemplatePlanRepo()}, nil)
			svc.accountTests = &AccountTestService{accountRepo: &qualitySupportAccounts{account: account}}
			svc.qualityModels = func(context.Context, int64) ([]string, error) { return []string{"gpt-6.1-sol"}, nil }
			_, err := svc.CreatePlan(context.Background(), multiModelQualityPlan(t, `["gpt-6.1-sol"]`))
			if kind == "allowlist" {
				require.Equal(t, "QUALITY_MODEL_NOT_ALLOWED", infraerrors.Reason(err))
				require.NotContains(t, err.Error(), "不支持检测模型")
			} else {
				require.ErrorContains(t, err, "不支持检测模型")
			}
		})
	}
}

func TestQualityLiveModelListCredentialErrorIsStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"token_revoked","message":"synthetic-private-provider-text"}}`))
	}))
	defer server.Close()
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	t.Cleanup(func() { chatgptCodexModelsURL = original })
	svc := &OpenAIGatewayService{}
	tests := &AccountTestService{openaiGatewayService: svc}
	_, err := tests.FetchOpenAIAccountModels(context.Background(), newCodexModelsTestAccount())
	require.Error(t, err)
	status, body := infraerrors.ToHTTP(err)
	require.Equal(t, http.StatusBadGateway, status, "upstream 401 must not expire admin login")
	require.Equal(t, "OPENAI_CODEX_MODELS_CREDENTIALS_INVALID", body.Reason)
	require.Equal(t, "401", body.Metadata["upstream_status"])
	require.Equal(t, "revoked", body.Metadata["credential_state"])
	require.NotContains(t, body.Message, "synthetic-private-provider-text")
	require.NotContains(t, err.Error(), "synthetic-private-provider-text")
	require.False(t, qualityModelUnsupported(err.Error()))
	require.True(t, IsRetryableCodexModelsManifestError(err))
}

func TestQualityLiveAuthenticationAndPermissionFailuresAreNotModelRejections(t *testing.T) {
	for _, message := range []string{
		`API returned 401: {"error":{"code":"model_not_found"}}`,
		`Authentication failed (401): model_not_supported`,
		`HTTP 403: model not supported`,
		`Model example is not supported by any configured account in this group`,
	} {
		require.False(t, qualityModelUnsupported(message), message)
	}
}

func TestQualityLiveModelListCredentialReplacementRecoversWithoutCachedFailure(t *testing.T) {
	calls := 0
	upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		calls++
		if req.Header.Get("Authorization") == "Bearer synthetic-old" {
			response := ordinaryModelsUpstreamResponse(`{"error":{"code":"token_revoked","message":"synthetic-private"}}`)
			response.StatusCode = 401
			return response, nil
		}
		return ordinaryModelsUpstreamResponse(`{"data":[{"id":"gpt-6.1-sol"}]}`), nil
	}}
	gateway := newCodexModelsAPIKeyTestService(upstream)
	tests := &AccountTestService{openaiGatewayService: gateway}
	account := newCodexModelsAPIKeyTestAccount("https://models.example/v1")
	account.Credentials["api_key"] = "synthetic-old"
	_, err := tests.FetchOpenAIAccountModels(context.Background(), account)
	require.Equal(t, "OPENAI_CODEX_MODELS_CREDENTIALS_INVALID", infraerrors.Reason(err))
	// This is a synthetic credential replacement, not a real refresh/login.
	account.Credentials["api_key"] = "synthetic-replaced"
	models, err := tests.FetchOpenAIAccountModels(context.Background(), account)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "gpt-6.1-sol", models[0].ID)
	_, err = tests.FetchOpenAIAccountModels(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "successful replacement shares cache; failed credentials are not cached")
}
