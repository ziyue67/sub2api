package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type multiGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
}

func (s multiGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	if group := s.groups[id]; group != nil {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

type multiGroupRateStub struct {
	UserGroupRateRepository
	rates  map[int64]float64
	denied map[int64][]string
}

func (s multiGroupRateStub) GetByUserAndGroup(_ context.Context, _, groupID int64) (*float64, error) {
	if rate, ok := s.rates[groupID]; ok {
		return &rate, nil
	}
	return nil, nil
}
func (s multiGroupRateStub) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	return nil, nil
}
func (s multiGroupRateStub) GetDeniedModelsByUserAndGroup(_ context.Context, _, groupID int64) ([]string, error) {
	return s.denied[groupID], nil
}

type multiGroupRouterStub map[int64]APIKeyGroupAvailability

func (s multiGroupRouterStub) ProbeAPIKeyGroup(_ context.Context, group *Group, _, _ string) (APIKeyGroupAvailability, error) {
	return s[group.ID], nil
}

func TestAPIKeyMultiGroupSelectionUsesActualGroupAndDoesNotMutateAuthSnapshot(t *testing.T) {
	primary := int64(1)
	groups := map[int64]*Group{
		1: {ID: 1, Status: StatusActive, RateMultiplier: 0.5},
		2: {ID: 2, Status: StatusActive, RateMultiplier: 0.8},
		3: {ID: 3, Status: StatusActive, RateMultiplier: 0.1},
	}
	key := &APIKey{ID: 11, UserID: 7, GroupID: &primary, GroupIDs: []int64{1, 2, 3}, Group: groups[1], User: &User{ID: 7, Balance: 10, UserGroupDeniedModels: []string{"original"}}}
	svc := &APIKeyService{groupRepo: multiGroupRepoStub{groups: groups}, userGroupRateRepo: multiGroupRateStub{rates: map[int64]float64{2: 0.2}}, groupRouter: multiGroupRouterStub{1: {Available: true}, 2: {Available: true}}}
	got, err := svc.SelectRequestGroup(context.Background(), key, []string{"gpt-5"}, "token", nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, int64(2), *got.GroupID)
	require.Same(t, groups[2], got.Group)
	require.Equal(t, key.GroupIDs, got.GroupIDs)
	require.Equal(t, int64(1), *key.GroupID)
	require.Equal(t, []string{"original"}, key.User.UserGroupDeniedModels)
	require.NotSame(t, key.User, got.User)

	// A lower price cannot override user-specific model restrictions.
	svc.userGroupRateRepo = multiGroupRateStub{rates: map[int64]float64{2: 0.2}, denied: map[int64][]string{2: {"gpt-5"}}}
	got, err = svc.SelectRequestGroup(context.Background(), key, []string{"gpt-5"}, "token", nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, int64(1), *got.GroupID)
}

func TestAPIKeyMultiGroupSelectionPrefersReadyCapacityAndRejectsRevokedGroups(t *testing.T) {
	primary := int64(1)
	groups := map[int64]*Group{1: {ID: 1, Status: StatusActive, RateMultiplier: 0.1}, 2: {ID: 2, Status: StatusActive, RateMultiplier: 0.3}, 3: {ID: 3, Status: StatusActive, RateMultiplier: 0, IsExclusive: true}}
	key := &APIKey{UserID: 7, GroupID: &primary, GroupIDs: []int64{1, 2, 3}, Group: groups[1], User: &User{ID: 7, Balance: 10}}
	svc := &APIKeyService{groupRepo: multiGroupRepoStub{groups: groups}, groupRouter: multiGroupRouterStub{1: {Available: true, Busy: true, Load: 1}, 2: {Available: true, Load: 0.1}, 3: {Available: true}}}
	got, err := svc.SelectRequestGroup(context.Background(), key, nil, "token", nil, nil, true)
	require.NoError(t, err)
	require.Equal(t, int64(2), *got.GroupID)

	_, err = svc.PinAPIKeyGroup(context.Background(), key, 3)
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	svc.groupRouter = multiGroupRouterStub{}
	_, err = svc.SelectRequestGroup(context.Background(), key, nil, "token", nil, nil, true)
	require.Error(t, err)
}

func TestNormalizeAPIKeyGroupIDsCompatibilityAndValidation(t *testing.T) {
	id := int64(7)
	ids, err := normalizeAPIKeyGroupIDs(&id, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, ids)
	for _, invalid := range [][]int64{{7, 7}, {7, 0}, {8, 7}} {
		_, err := normalizeAPIKeyGroupIDs(&id, invalid)
		require.Error(t, err)
	}
	svc := &APIKeyService{}
	key := &APIKey{ID: 1, UserID: 7, GroupID: &id, GroupIDs: []int64{7, 8}, User: &User{ID: 7}}
	roundtrip := svc.snapshotToAPIKey("unused", svc.snapshotFromAPIKey(context.Background(), key))
	require.Equal(t, []int64{7, 8}, roundtrip.GroupIDs)
}
