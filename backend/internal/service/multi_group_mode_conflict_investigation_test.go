package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

// Regression: existing mixed keys must not let the cheapest-group selector
// override explicit priority, disabled entries or failed-route cooldowns.
func TestInvestigationMultiGroupModeConflict(t *testing.T) {
	for _, scenario := range []string{"priority", "cooldown", "disabled_route"} {
		t.Run(scenario, func(t *testing.T) {
			primary := int64(1)
			groups := map[int64]*Group{
				1: {ID: 1, Status: StatusActive, RateMultiplier: 1},
				2: {ID: 2, Status: StatusActive, RateMultiplier: 0.5},
			}
			key := &APIKey{ID: 11, UserID: 7, GroupID: &primary, GroupIDs: []int64{1, 2}, Group: groups[1], User: &User{ID: 7, Balance: 10}, GroupRoutes: []domain.APIKeyGroupRoute{
				{GroupID: 1, Priority: 1, Weight: 1, Enabled: true},
				{GroupID: 2, Priority: 2, Weight: 1, Enabled: true, CooldownSeconds: 300},
			}}
			svc := &APIKeyService{groupRepo: multiGroupRepoStub{groups: groups}, groupRouter: multiGroupRouterStub{1: {Available: true}, 2: {Available: true}}}
			if scenario == "cooldown" {
				svc.MarkGroupRouteFailed(key, 2)
				require.True(t, svc.groupRouteExcludedOrCooling(key.ID, 2, nil))
			}
			if scenario == "disabled_route" {
				key.GroupRoutes[1].Enabled = false
			}
			require.NoError(t, svc.ApplySelectedGroupRoute(context.Background(), key))
			require.Equal(t, int64(1), *key.GroupID)
			got, err := svc.SelectRequestGroup(context.Background(), key, []string{"gpt-5"}, "token", nil, nil, true)
			require.NoError(t, err)
			t.Logf("scenario=%s primary=%d actual=%d routes_retained=%d", scenario, *key.GroupID, *got.GroupID, len(got.GroupRoutes))
			require.Equal(t, int64(1), *got.GroupID, "route priority/cooldown/disabled state must not be silently overridden by additional groups")
		})
	}
}
