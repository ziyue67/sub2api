package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCacheCreationBillingAdminOnly(t *testing.T) {
	log := &service.UsageLog{CacheCreationTokens: 100, CacheCreation1hTokens: 100, ActualCost: 0.01,
		CacheCreationBilling: &service.CacheCreationBillingAudit{Version: 1, Enabled: true, Status: "converted", Original5mTokens: 100, ConvertedTokens: 100}}
	user, err := json.Marshal(UsageLogFromService(log))
	require.NoError(t, err)
	require.Contains(t, string(user), `"cache_creation_1h_tokens":100`)
	require.NotContains(t, string(user), "cache_creation_billing")
	require.NotContains(t, string(user), "original_5m")
	require.NotContains(t, string(user), "converted")
	observer, err := json.Marshal(UsageLogFromServiceObserver(log))
	require.NoError(t, err)
	require.NotContains(t, string(observer), "cache_creation_billing")
	admin, err := json.Marshal(UsageLogFromServiceAdmin(log))
	require.NoError(t, err)
	require.Contains(t, string(admin), `"original_5m_tokens":100`)
	require.Contains(t, string(admin), `"status":"converted"`)
}
