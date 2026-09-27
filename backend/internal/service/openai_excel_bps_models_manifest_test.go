package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func excelBPSManifestAccount() Account {
	account := *excelAccount()
	account.Credentials["model_mapping"] = map[string]any{
		"gpt-6-astra": "gpt-6-astra", "public-astra": "gpt-6-astra", "gpt-5.6-sol": "gpt-5.6-sol",
	}
	account.Extra["openai_excel_bps_models"] = []string{"gpt-6-astra"}
	models := make(map[string]UpstreamModelMetadata)
	for _, id := range []string{"gpt-6-astra", "gpt-5.6-sol"} {
		models[id] = UpstreamModelMetadata{ID: id, CodexToolCapabilities: map[string]json.RawMessage{
			"multi_agent_version": json.RawMessage(`"v2"`), "multi_agent_reasoning_effort": json.RawMessage(`"xhigh"`),
			"apply_patch_tool_type": json.RawMessage(`"freeform"`), "supports_search_tool": json.RawMessage("true"),
		}}
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: models})
	return account
}

func TestExcelBPSManifestCapabilitiesFollowMappedOAuthRoute(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			account := excelBPSManifestAccount()
			account.Type = accountType
			body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"gpt-6-astra", "public-astra", "gpt-5.6-sol"}, []Account{account}, nil, nil, true)
			require.NoError(t, err)
			for _, model := range decodeCodexManifestModels(t, body) {
				if accountType == AccountTypeOAuth && model["slug"] != "gpt-5.6-sol" {
					require.Contains(t, model, "multi_agent_version")
					require.Nil(t, model["multi_agent_version"])
					require.Nil(t, model["multi_agent_reasoning_effort"])
				} else {
					require.Equal(t, "v2", model["multi_agent_version"])
					require.Equal(t, "xhigh", model["multi_agent_reasoning_effort"])
				}
				require.Equal(t, "freeform", model["apply_patch_tool_type"])
				require.Equal(t, true, model["supports_search_tool"])
			}
			// Reading the guarded catalog must not rewrite the account snapshot.
			metadata, ok := account.GetUpstreamModelMetadata("gpt-6-astra")
			require.True(t, ok)
			require.Equal(t, `"v2"`, string(metadata.CodexToolCapabilities["multi_agent_version"]))
		})
	}
}

func TestExcelBPSManifestMixedRoutesAndExplicitRouting(t *testing.T) {
	bps := excelBPSManifestAccount()
	native := excelBPSManifestAccount()
	native.ID = 301
	native.Extra["openai_excel_bps"] = false
	for _, routed := range []bool{false, true} {
		group := &Group{ID: 11, Platform: PlatformOpenAI, ModelRoutingEnabled: routed, ModelRouting: map[string][]int64{"public-astra": {native.ID}}}
		body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"public-astra"}, []Account{bps, native}, group, nil, true)
		require.NoError(t, err)
		model := decodeCodexManifestModels(t, body)[0]
		if routed {
			require.Equal(t, "v2", model["multi_agent_version"])
		} else {
			require.Nil(t, model["multi_agent_version"])
		}
	}
}

func TestExcelBPSManifestFetchedAndPinnedCatalogUseFinalETag(t *testing.T) {
	const source = `{"models":[{"slug":"public-astra","multi_agent_version":"v2","multi_agent_reasoning_effort":"xhigh","apply_patch_tool_type":"freeform","unknown":{"kept":true}},{"slug":"gpt-5.6-sol","multi_agent_version":"v2","multi_agent_reasoning_effort":"high"}],"metadata":{"version":1}}`
	for _, pinned := range []bool{false, true} {
		account := excelBPSManifestAccount()
		group := &Group{ID: 11, Platform: PlatformOpenAI}
		group.CodexModelsManifestConfig.Enabled = pinned
		svc := &OpenAIGatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{11: {account}}}}
		cacheBody := []byte(source)
		manifest := &OpenAIModelsResponse{Body: cacheBody, ETag: codexModelsManifestBodyETag(cacheBody)}
		oldETag := manifest.ETag
		require.NoError(t, svc.MergeGroupConfiguredCodexModels(context.Background(), group, manifest, oldETag))
		require.False(t, manifest.NotModified, "stale native capability ETag must not return 304")
		require.Equal(t, []byte(source), cacheBody, "do not contaminate the shared upstream cache")
		require.NotEqual(t, oldETag, manifest.ETag)
		require.Equal(t, codexModelsManifestBodyETag(manifest.Body), manifest.ETag)
		models := decodeCodexManifestModels(t, manifest.Body)
		require.Nil(t, models[0]["multi_agent_version"])
		require.Nil(t, models[0]["multi_agent_reasoning_effort"])
		require.Equal(t, "freeform", models[0]["apply_patch_tool_type"])
		require.Equal(t, map[string]any{"kept": true}, models[0]["unknown"])
		require.Equal(t, "v2", models[1]["multi_agent_version"])
		next := &OpenAIModelsResponse{Body: []byte(source), ETag: oldETag}
		require.NoError(t, svc.MergeGroupConfiguredCodexModels(context.Background(), group, next, manifest.ETag))
		require.True(t, next.NotModified)
		require.Empty(t, next.Body)
	}
}

func TestExcelBPSManifestRestrictionPreservesNativeModels(t *testing.T) {
	body := []byte(`{"models":[{"slug":"public-astra","multi_agent_version":"v2","multi_agent_reasoning_effort":"high"}]}`)
	bps := excelBPSManifestAccount()
	native := excelBPSManifestAccount()
	native.ID = 301
	native.Extra["openai_excel_bps"] = false
	group := &Group{ID: 11, Platform: PlatformOpenAI, ModelRoutingEnabled: true, ModelRouting: map[string][]int64{"public-astra": {301}}}
	for _, accounts := range [][]Account{{native}, {bps, native}} {
		result, changed, err := restrictExcelBPSCodexModelsManifest(body, accounts, group)
		require.NoError(t, err)
		require.False(t, changed)
		require.True(t, bytes.Equal(body, result))
	}
	bps.Extra["openai_excel_bps_models"] = []string{}
	result, changed, err := restrictExcelBPSCodexModelsManifest(body, []Account{bps}, nil)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, result)
}

func TestExcelBPSManifestConflictingAliasDoesNotRestoreBundledV2(t *testing.T) {
	bps := excelBPSManifestAccount()
	peer := excelBPSManifestAccount()
	peer.ID = 301
	peer.Extra["openai_excel_bps"] = false
	peer.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-5.6-sol"}
	body, err := buildCodexModelsManifestForAccounts(PlatformOpenAI, []string{"gpt-6-astra"}, []Account{bps, peer}, nil, nil, true)
	require.NoError(t, err)
	model := decodeCodexManifestModels(t, body)[0]
	require.Nil(t, model["multi_agent_version"])
	require.Nil(t, model["multi_agent_reasoning_effort"])
}
