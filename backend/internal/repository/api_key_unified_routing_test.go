package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUnifiedRoutingPersistenceAndAtomicRollback(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "unified-sqlite@test.invalid")
	a, err := client.Group.Create().SetName("unified-a").Save(ctx)
	require.NoError(t, err)
	b, err := client.Group.Create().SetName("unified-b").Save(ctx)
	require.NoError(t, err)
	policy := &domain.APIKeyGroupRouting{Strategy: service.APIKeyRoutingPriority, Failover: true, Routes: []domain.APIKeyGroupRoute{{GroupID: a.ID, Priority: 1, Weight: 1, Enabled: true}, {GroupID: b.ID, Priority: 2, Weight: 3, Enabled: true}}}
	key := &service.APIKey{UserID: user.ID, Key: "sk-unified-sqlite-test", Name: "original", Status: service.StatusActive, GroupID: &a.ID, GroupIDs: []int64{a.ID, b.ID}, GroupRoutes: policy.Routes, GroupRouting: policy}
	require.NoError(t, repo.Create(ctx, key))
	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, policy, got.GroupRouting)
	got.GroupIDs = []int64{a.ID, 999999999}
	got.GroupRouting.Failover = false
	require.Error(t, repo.Update(ctx, got, service.APIKeyUpdateFields{GroupID: true, GroupRouting: true}))
	unchanged, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.True(t, unchanged.GroupRouting.Failover, "failed binding update must also roll back policy")
	require.Equal(t, []int64{a.ID, b.ID}, unchanged.GroupIDs)
	unchanged.Name = "renamed"
	require.NoError(t, repo.Update(ctx, unchanged, service.APIKeyUpdateFields{Name: true}))
	unchanged, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, policy, unchanged.GroupRouting)
}
