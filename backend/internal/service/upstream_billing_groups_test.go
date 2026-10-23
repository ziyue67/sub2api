package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func multiGroupBillingBody(t *testing.T) []byte {
	t.Helper()
	groups := []map[string]any{}
	for i, rate := range []float64{0.18, 0.3} {
		groups = append(groups, map[string]any{
			"object": "sub2api.key_billing", "schema_version": 1, "billing_scope": "token",
			"group_id": i + 1, "group_name": []string{"Economy", "Astra"}[i], "platform": "openai", "status": "active",
			"group_rate_multiplier": rate, "resolved_rate_multiplier": rate,
			"peak_rate_enabled": false, "effective_rate_multiplier": rate, "observed_at": "2026-10-20T00:00:00Z",
			"api_key": "must-not-be-persisted",
		})
	}
	body, err := json.Marshal(map[string]any{
		"object": "sub2api.key_billing", "schema_version": 2, "billing_scope": "token", "billing_mode": "multi_group", "groups": groups,
		"observed_at": "2026-10-20T00:00:00Z", "resolved_rate_multiplier": 0.01, "effective_rate_multiplier": 0.01,
	})
	require.NoError(t, err)
	return body
}

func TestMultiGroupUpstreamDeclarationRetainsGroupsWithoutScalarSync(t *testing.T) {
	body := multiGroupBillingBody(t)
	data, err := parseUpstreamBillingProbeResponse(body)
	require.NoError(t, err)
	require.Equal(t, "multi_group", data["billing_mode"])
	require.Equal(t, 0.18, data["min_rate_multiplier"])
	require.Equal(t, 0.3, data["max_rate_multiplier"])
	require.NotContains(t, data, "resolved_rate_multiplier")
	require.Len(t, data["groups"], 2)
	sanitized, err := json.Marshal(data)
	require.NoError(t, err)
	require.NotContains(t, string(sanitized), "must-not-be-persisted")
	require.NotContains(t, string(sanitized), "api_key")

	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	account := &Account{ID: 1, Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true, UpstreamBillingRateSyncEnabledExtraKey: true}}
	snapshot, sync := upstreamBillingRateSnapshotFromResult(account, 30, now, upstreamBillingProbeHTTPResult{body: body, statusCode: http.StatusOK}, 0.5)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Nil(t, sync)
	require.Nil(t, snapshot.SyncedRateMultiplier)
	_, ok := snapshot.CostMultiplierToSync()
	require.False(t, ok)
	// Even a manually supplied scalar alongside a dynamic mode is not authoritative.
	data["resolved_rate_multiplier"] = 0.01
	_, ok = upstreamBillingProbeSyncRate(data)
	require.False(t, ok)
	_, ok = upstreamBillingRateAt(data, now)
	require.False(t, ok)
}

func TestMultiGroupUpstreamDeclarationRejectsInvalidAndDuplicateGroups(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(doc map[string]any) { doc["groups"] = []any{} },
		func(doc map[string]any) {
			groups, ok := doc["groups"].([]any)
			require.True(t, ok)
			groups[1] = groups[0]
		},
		func(doc map[string]any) {
			groups, ok := doc["groups"].([]any)
			require.True(t, ok)
			group, ok := groups[0].(map[string]any)
			require.True(t, ok)
			group["effective_rate_multiplier"] = 100
		},
		func(doc map[string]any) {
			groups, ok := doc["groups"].([]any)
			require.True(t, ok)
			group, ok := groups[0].(map[string]any)
			require.True(t, ok)
			group["schema_version"] = 2
		},
	} {
		var doc map[string]any
		require.NoError(t, json.Unmarshal(multiGroupBillingBody(t), &doc))
		change(doc)
		body, err := json.Marshal(doc)
		require.NoError(t, err)
		_, err = parseUpstreamBillingProbeResponse(body)
		require.Error(t, err)
	}
}

func TestMultiGroupUpstreamDeclarationPreservesGroupStatus(t *testing.T) {
	for _, status := range []string{StatusActive, "inactive", StatusDisabled, "unknown"} {
		t.Run(status, func(t *testing.T) {
			body := strings.Replace(string(multiGroupBillingBody(t)), `"status":"active"`, `"status":"`+status+`"`, 1)
			data, err := parseUpstreamBillingProbeResponse([]byte(body))
			if status != StatusActive && status != "inactive" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			groups, ok := data["groups"].([]map[string]any)
			require.True(t, ok)
			require.Equal(t, status, groups[0]["status"])
		})
	}
}
