//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func unifiedRoutingFixture() (*APIKeyService, *APIKey) {
	groups := map[int64]*Group{
		1: {ID: 1, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1},
		2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.5},
		3: {ID: 3, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.2},
	}
	svc := &APIKeyService{groupRepo: multiGroupRepoStub{groups: groups}, groupRouter: multiGroupRouterStub{1: {Available: true}, 2: {Available: true}, 3: {Available: true}}}
	key := &APIKey{ID: 41, UserID: 7, User: &User{ID: 7, Balance: 10}, QuotaUsed: 12.34}
	applyAPIKeyGroupRouting(key, &domain.APIKeyGroupRouting{Strategy: APIKeyRoutingLowestRate, Failover: true, Routes: []domain.APIKeyGroupRoute{
		{GroupID: 1, Priority: 1, Weight: 1, Enabled: true, CooldownSeconds: 60},
		{GroupID: 2, Priority: 2, Weight: 3, Enabled: true, CooldownSeconds: 60},
		{GroupID: 3, Priority: 3, Weight: 1, Enabled: true, CooldownSeconds: 60},
	}})
	return svc, key
}

func TestUnifiedRoutingFailoverUsesSameFiltersAndPreservesSnapshot(t *testing.T) {
	svc, key := unifiedRoutingFixture()
	svc.userGroupRateRepo = multiGroupRateStub{denied: map[int64][]string{2: {"gpt-5"}}}
	selected, err := svc.SelectRequestGroup(context.Background(), key, []string{"gpt-5"}, "token", nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, int64(3), *selected.GroupID)
	require.NoError(t, svc.MarkRequestGroupRouteFailed(context.Background(), selected, 3))
	ok, err := svc.ApplyNextGroupRoute(context.Background(), selected, map[int64]struct{}{3: {}})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(1), *selected.GroupID, "denied group must also be skipped on retry")
	require.Equal(t, float64(1), selected.Group.RateMultiplier, "billing input follows actual selected group")
	require.Equal(t, 12.34, selected.QuotaUsed, "routing does not debit usage")
	require.Equal(t, int64(1), *key.GroupID, "cached auth key remains unchanged")
	require.Nil(t, key.requestGroupRouting)
	selected.GroupRouting.Routes[0].Enabled = false
	require.True(t, key.GroupRouting.Routes[0].Enabled, "request must not mutate cached policy")
}

func TestUnifiedRoutingPriorityWeightsAndFailoverOff(t *testing.T) {
	svc, key := unifiedRoutingFixture()
	key.GroupRouting.Strategy = APIKeyRoutingPriority
	key.GroupRouting.Routes[1].Priority = 1
	key.GroupRouting.Failover = false
	counts := map[int64]int{}
	for i := 0; i < 40; i++ {
		got, err := svc.SelectRequestGroup(context.Background(), key, nil, "token", nil, nil, false)
		require.NoError(t, err)
		counts[*got.GroupID]++
		ok, err := svc.ApplyNextGroupRoute(context.Background(), got, map[int64]struct{}{*got.GroupID: {}})
		require.NoError(t, err)
		require.False(t, ok)
	}
	require.Equal(t, map[int64]int{1: 10, 2: 30}, counts, "cheap lower-priority group must not override weighted tier")
}

func TestUnifiedRoutingEligibilityCapacityAndDisabled(t *testing.T) {
	for _, scenario := range []string{"disabled", "cooldown", "busy", "no-capacity", "all-disabled", "all-busy", "image"} {
		t.Run(scenario, func(t *testing.T) {
			svc, key := unifiedRoutingFixture()
			kind := "token"
			switch scenario {
			case "disabled":
				key.GroupRouting.Routes[2].Enabled = false
			case "cooldown":
				svc.MarkGroupRouteFailed(key, 3)
			case "busy":
				svc.groupRouter.(multiGroupRouterStub)[3] = APIKeyGroupAvailability{Available: true, Busy: true}
			case "no-capacity":
				svc.groupRouter.(multiGroupRouterStub)[3] = APIKeyGroupAvailability{}
			case "all-disabled":
				for i := range key.GroupRouting.Routes {
					key.GroupRouting.Routes[i].Enabled = false
				}
			case "all-busy":
				for i := int64(1); i <= 3; i++ {
					svc.groupRouter.(multiGroupRouterStub)[i] = APIKeyGroupAvailability{Available: true, Busy: true}
				}
			case "image":
				kind = "image"
			}
			got, err := svc.SelectRequestGroup(context.Background(), key, nil, kind, nil, nil, false)
			if scenario == "all-disabled" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if scenario == "all-busy" || scenario == "image" {
				require.Equal(t, int64(3), *got.GroupID)
			} else {
				require.Equal(t, int64(2), *got.GroupID)
			}
		})
	}
}

func TestUnifiedRoutingValidationAndLegacyWriteProtection(t *testing.T) {
	_, key := unifiedRoutingFixture()
	for _, mutate := range []func(*domain.APIKeyGroupRouting){
		func(p *domain.APIKeyGroupRouting) { p.Strategy = "unknown" },
		func(p *domain.APIKeyGroupRouting) { p.Routes = nil },
		func(p *domain.APIKeyGroupRouting) { p.Routes[1].GroupID = p.Routes[0].GroupID },
		func(p *domain.APIKeyGroupRouting) { p.Routes[0].Weight = 1000001 },
		func(p *domain.APIKeyGroupRouting) { p.Routes[0].CooldownSeconds = 86401 },
	} {
		p := cloneAPIKeyGroupRouting(key.GroupRouting)
		mutate(p)
		_, err := normalizeAPIKeyGroupRouting(p)
		require.Error(t, err)
	}
	svc, repo := newUpdateFieldsAPIKeyService(key)
	name := "unchanged routing"
	got, err := svc.Update(context.Background(), key.ID, key.UserID, UpdateAPIKeyRequest{Name: &name})
	require.NoError(t, err)
	require.Equal(t, key.GroupRouting, got.GroupRouting)
	require.Equal(t, APIKeyUpdateFields{Name: true}, repo.updateFields[0])
	_, err = svc.Update(context.Background(), key.ID, key.UserID, UpdateAPIKeyRequest{GroupID: key.GroupID})
	require.ErrorIs(t, err, errAPIKeyRoutingConflict)
}

type unifiedGroupRepo struct{ multiGroupRepoStub }

func (r unifiedGroupRepo) GetByID(ctx context.Context, id int64) (*Group, error) {
	return r.GetByIDLite(ctx, id)
}

func TestUnifiedRoutingUpdateWritesPolicyAndBindingsAtomically(t *testing.T) {
	original, key := unifiedRoutingFixture()
	svc, repo := newUpdateFieldsAPIKeyService(key)
	svc.userRepo = &userRepoStub{user: key.User}
	svc.groupRepo = unifiedGroupRepo{original.groupRepo.(multiGroupRepoStub)}
	policy := cloneAPIKeyGroupRouting(key.GroupRouting)
	policy.Routes = policy.Routes[1:]
	got, err := svc.Update(context.Background(), key.ID, key.UserID, UpdateAPIKeyRequest{GroupRouting: policy})
	require.NoError(t, err)
	require.Equal(t, int64(2), *got.GroupID)
	require.Equal(t, []int64{2, 3}, got.GroupIDs)
	require.Equal(t, policy.Routes, got.GroupRoutes)
	require.Equal(t, APIKeyUpdateFields{GroupID: true, GroupRoutes: true, GroupRouting: true}, repo.updateFields[0])
	require.True(t, key.GroupRouting.Routes[0].Enabled)
}

func TestUnifiedRoutingFailoverCannotCrossHandlerPlatform(t *testing.T) {
	svc, key := unifiedRoutingFixture()
	selected, err := svc.SelectRequestGroup(context.Background(), key, nil, "token", nil, nil, false)
	require.NoError(t, err)
	svc.groupRepo.(multiGroupRepoStub).groups[2].Platform = PlatformAnthropic
	switched, err := svc.ApplyNextGroupRoute(context.Background(), selected, map[int64]struct{}{3: {}})
	require.NoError(t, err)
	require.True(t, switched)
	require.Equal(t, int64(1), *selected.GroupID)
	switched, err = svc.ApplyNextGroupRoute(context.Background(), selected, map[int64]struct{}{3: {}, 1: {}})
	require.NoError(t, err)
	require.False(t, switched)
}

type unifiedCooldownStub struct {
	APIKeyCache
	cooling map[int64]bool
	err     error
}

func (c *unifiedCooldownStub) APIKeyRouteCooling(_ context.Context, _ int64, id int64) (bool, error) {
	return c.cooling[id], c.err
}
func (c *unifiedCooldownStub) SetAPIKeyRouteCooldown(_ context.Context, _ int64, id int64, _ time.Duration) error {
	c.cooling[id] = true
	return c.err
}

func TestUnifiedRoutingSharedCooldownAndAuthSnapshot(t *testing.T) {
	svc, key := unifiedRoutingFixture()
	shared := &unifiedCooldownStub{cooling: map[int64]bool{}}
	svc.cache = shared
	require.NoError(t, svc.MarkRequestGroupRouteFailed(context.Background(), key, 3))
	other, _ := unifiedRoutingFixture()
	other.cache = shared
	got, err := other.SelectRequestGroup(context.Background(), key, nil, "token", nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), *got.GroupID)
	shared.err = errors.New("redis unavailable")
	_, err = other.SelectRequestGroup(context.Background(), key, nil, "token", nil, nil, false)
	require.Error(t, err, "must not silently ignore an unavailable shared cooldown store")
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	require.Equal(t, key.GroupRouting, snapshot.GroupRouting)
	snapshot.GroupRouting.Routes[0].Enabled = false
	require.True(t, key.GroupRouting.Routes[0].Enabled)
}
