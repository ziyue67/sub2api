package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMultiGroupKeyBillingDeclarationHasPerGroupRatesWithoutTopLevelScalar(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	groups := []keyBillingGroup{
		{GroupID: 1, GroupName: "Economy", keyBillingInfoResponse: keyBillingInfoResponse{EffectiveRateMultiplier: 0.18}},
		{GroupID: 2, GroupName: "Astra", keyBillingInfoResponse: keyBillingInfoResponse{EffectiveRateMultiplier: 0.3}},
	}
	declaration := buildMultiGroupKeyBillingInfo(groups, now)
	body, err := json.Marshal(declaration)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, float64(2), got["schema_version"])
	require.Equal(t, "multi_group", got["billing_mode"])
	require.Equal(t, 0.18, got["min_rate_multiplier"])
	require.Equal(t, 0.3, got["max_rate_multiplier"])
	require.NotContains(t, got, "resolved_rate_multiplier")
	require.NotContains(t, got, "effective_rate_multiplier")
	require.Len(t, got["groups"], 2)
}
