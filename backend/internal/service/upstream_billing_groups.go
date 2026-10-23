package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Whitelist every group field just as strictly as a v1 declaration. Extra
// upstream fields, including credentials, must never enter account snapshots.
func parseMultiGroupUpstreamBillingResponse(body []byte) (map[string]any, error) {
	var response struct {
		Object        string            `json:"object"`
		SchemaVersion int               `json:"schema_version"`
		BillingScope  string            `json:"billing_scope"`
		BillingMode   string            `json:"billing_mode"`
		Groups        []json.RawMessage `json:"groups"`
		ObservedAt    string            `json:"observed_at"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.Object != "sub2api.key_billing" || response.SchemaVersion != 2 || response.BillingScope != "token" || response.BillingMode != "multi_group" || len(response.Groups) == 0 || len(response.Groups) > maxAPIKeyGroups {
		return nil, fmt.Errorf("invalid multi-group billing declaration")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, response.ObservedAt)
	if err != nil || observedAt.IsZero() {
		return nil, fmt.Errorf("invalid observed_at")
	}
	groups := make([]map[string]any, 0, len(response.Groups))
	seen := make(map[int64]bool, len(response.Groups))
	minRate, maxRate := math.Inf(1), 0.0
	for _, raw := range response.Groups {
		var identity struct {
			GroupID       int64  `json:"group_id"`
			GroupName     string `json:"group_name"`
			Platform      string `json:"platform"`
			Status        string `json:"status"`
			SchemaVersion int    `json:"schema_version"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return nil, err
		}
		if identity.GroupID <= 0 || seen[identity.GroupID] || len(identity.GroupName) > 1024 || len(identity.Platform) > 64 || identity.SchemaVersion != 1 || (identity.Status != StatusActive && identity.Status != "inactive") {
			return nil, fmt.Errorf("invalid multi-group billing entry")
		}
		seen[identity.GroupID] = true
		data, err := parseUpstreamBillingProbeResponse(raw)
		if err != nil {
			return nil, err
		}
		if data["observed_at"] != observedAt.UTC().Format(time.RFC3339Nano) {
			return nil, fmt.Errorf("inconsistent group observed_at")
		}
		data["group_id"], data["group_name"] = identity.GroupID, strings.TrimSpace(identity.GroupName)
		data["platform"], data["status"] = identity.Platform, identity.Status
		groups = append(groups, data)
		rate, ok := data["effective_rate_multiplier"].(float64)
		if !ok {
			return nil, fmt.Errorf("invalid group effective multiplier")
		}
		minRate, maxRate = math.Min(minRate, rate), math.Max(maxRate, rate)
	}
	return map[string]any{
		"object": "sub2api.key_billing", "schema_version": 2, "billing_scope": "token", "billing_mode": "multi_group",
		"groups": groups, "min_rate_multiplier": minRate, "max_rate_multiplier": maxRate,
		"observed_at": observedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}
