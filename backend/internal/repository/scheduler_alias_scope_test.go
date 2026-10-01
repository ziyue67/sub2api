package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCandidateSnapshotPreservesAliasScope(t *testing.T) {
	credentials := map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.6-sol"}, "model_mapping_mode": "aliases", "access_token": "test-only-token"}
	account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: filterSchedulerCredentials(credentials)}
	require.True(t, account.IsModelSupported("gpt-6-sol"), "lightweight candidate filtering must retain alias-mode admission")
	require.Equal(t, "gpt-5.6-sol", account.GetMappedModel("gpt-5.4"))
	require.NotContains(t, account.Credentials, "access_token")
	account.Credentials["model_mapping_mode"] = "whitelist"
	require.False(t, account.IsModelSupported("gpt-6-sol"))
}
