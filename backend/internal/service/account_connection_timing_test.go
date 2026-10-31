//go:build unit

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This regression deliberately uses only pre-existing APIs so CI can run the
// same test against the unmodified production baseline in a separate directory.
func TestAccountConnectionTimingLifecycle(t *testing.T) {
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(kind, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformAnthropic, Type: kind, Extra: map[string]any{"synthetic_ui_test": true}}
			svc := &AccountTestService{accountRepo: &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{1: account}}}}
			c, rec := newTestContext()
			require.NoError(t, svc.TestAccountConnection(c, 1, "synthetic-model", "", ""))
			var timings []map[string]any
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var event map[string]any
				require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
				if event["type"] == "timing" {
					timings = append(timings, event["timing"].(map[string]any))
				}
			}
			require.Len(t, timings, 2, "one start and one final timing event must accompany legacy events")
			require.Equal(t, "running", timings[0]["outcome"])
			require.Equal(t, "success", timings[1]["outcome"])
			require.NotEmpty(t, timings[1]["finished_at"])
			require.GreaterOrEqual(t, timings[1]["total_ms"], float64(0))
			require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
		})
	}
}
