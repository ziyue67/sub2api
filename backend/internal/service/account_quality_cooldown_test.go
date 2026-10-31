package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQualityModelCooldownSchedulingAfterDeadline(t *testing.T) {
	for _, tc := range []struct {
		reason  string
		blocked bool
	}{
		{"quality_rule:7", true},
		{"upstream_429", false},
		{"quality_rule:0", false},
		{"quality_rule:invalid", false},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			a := &Account{Platform: PlatformOpenAI, Status: "active", Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}},
				Extra: map[string]any{"model_rate_limits": map[string]any{"target": map[string]any{
					"reason": tc.reason, "rate_limit_reset_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
				}}}}
			require.Equal(t, !tc.blocked, a.IsSchedulableForModel("alias"))
			require.Equal(t, !tc.blocked, a.IsSchedulableForModel("target"))
			require.True(t, a.IsSchedulableForModel("other"))
			require.Equal(t, tc.blocked, a.isRateLimitActiveForKey("target"))
			probe := context.WithValue(context.Background(), qualityProbeContextKey{}, true)
			require.True(t, a.IsSchedulableForModelWithContext(probe, "alias"))
			if tc.blocked {
				a.Platform = PlatformAntigravity
				a.Extra["allow_overages"] = true
				require.False(t, a.IsSchedulableForModel("target"), "credits cannot bypass a quality cooldown")
			}
		})
	}
}
