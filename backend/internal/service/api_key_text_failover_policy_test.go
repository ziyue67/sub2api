//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompatibleGroupFailoverKeepsPolicyGates(t *testing.T) {
	for _, scenario := range []string{"allowed", "off", "no-opt-in", "disabled", "cooldown", "denied-model", "unavailable", "admission-rejected", "image", "no-admission"} {
		t.Run(scenario, func(t *testing.T) {
			svc, key := unifiedRoutingFixture()
			svc.groupRepo.(multiGroupRepoStub).groups[3].Platform = PlatformDeepseek
			key.GroupRouting.Routes[1].Enabled = false
			kind := "token"
			if scenario == "image" {
				kind = "image"
			}
			selected, err := svc.SelectRequestGroup(context.Background(), key, []string{"deepseek-v4.1-flash"}, kind, nil, func(k *APIKey) bool {
				return scenario != "admission-rejected" || *k.GroupID == 3
			}, true)
			require.NoError(t, err)
			require.Equal(t, int64(3), *selected.GroupID)
			ctx := WithOpenAITextGroupFailover(context.Background(), []byte(`{"messages":[]}`))
			switch scenario {
			case "off":
				selected.GroupRouting.Failover = false
			case "no-opt-in":
				ctx = context.Background()
			case "disabled":
				selected.GroupRouting.Routes[0].Enabled = false
			case "cooldown":
				svc.MarkGroupRouteFailed(selected, 1)
			case "denied-model":
				svc.userGroupRateRepo = multiGroupRateStub{denied: map[int64][]string{1: {"deepseek-v4.1-flash"}}}
			case "unavailable":
				svc.groupRouter.(multiGroupRouterStub)[1] = APIKeyGroupAvailability{}
			case "no-admission":
				selected.requestGroupRouting = nil
			}
			switched, err := svc.ApplyNextGroupRoute(ctx, selected, map[int64]struct{}{3: {}})
			require.NoError(t, err)
			require.Equal(t, scenario == "allowed", switched)
			if switched {
				require.Equal(t, int64(1), *selected.GroupID)
				require.Equal(t, float64(1), selected.Group.RateMultiplier)
			} else {
				require.Equal(t, int64(3), *selected.GroupID)
			}
			require.Equal(t, 12.34, selected.QuotaUsed, "selection alone never debits usage")
		})
	}
}
