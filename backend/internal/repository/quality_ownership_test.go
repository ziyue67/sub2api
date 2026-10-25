package repository

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQualityOwnershipClassifiesWithoutExposingArbitraryEntryFields(t *testing.T) {
	expected := json.RawMessage(`{"reason":"quality_rule:7","isolated_at":"2026-01-01T00:00:00Z","private_field":"must-never-serialize"}`)
	cases := []struct {
		name       string
		current    json.RawMessage
		status     string
		relinquish bool
	}{
		{"missing", nil, "missing", true},
		{"matches", expected, "managed", false},
		{"other rule", json.RawMessage(`{"reason":"quality_rule:9"}`), "external_owner", true},
		{"administrator", json.RawMessage(`{"reason":"administrator text containing private material"}`), "external_owner", true},
		{"same marker changed", json.RawMessage(`{"reason":"quality_rule:7","isolated_at":"2026-02-01T00:00:00Z"}`), "changed_same_owner", false},
		{"malformed", json.RawMessage(`[]`), "unrecognized", false},
		{"missing marker", json.RawMessage(`{"isolated_at":"unchanged"}`), "unrecognized", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := modelOwnershipEntry("test-model", expected, tc.current, 7)
			require.Equal(t, tc.status, entry.Status)
			require.Equal(t, tc.relinquish, entry.CanRelinquish)
			raw, err := json.Marshal(entry)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "must-never-serialize")
			require.NotContains(t, string(raw), "administrator text")
			require.NotContains(t, string(raw), "private_field")
		})
	}
}
func TestQualityOwnershipFenceIsCanonicalAndIncludesHiddenContent(t *testing.T) {
	a := json.RawMessage(`{"reason":"quality_rule:7","isolated_at":"a"}`)
	b := json.RawMessage(`{"isolated_at":"a","reason":"quality_rule:7"}`)
	require.Equal(t, qualityDigest(canonicalQualityEntry(a)), qualityDigest(canonicalQualityEntry(b)))
	require.NotEqual(t, qualityDigest(canonicalQualityEntry(a)), qualityDigest(canonicalQualityEntry(json.RawMessage(`{"reason":"quality_rule:7","isolated_at":"b"}`))))
	require.Equal(t, []string{"other_fields"}, qualityChangedFields(json.RawMessage(`{"secret_name":"old"}`), json.RawMessage(`{"secret_name":"new"}`)))
}
