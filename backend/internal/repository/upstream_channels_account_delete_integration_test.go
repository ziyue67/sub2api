//go:build integration

package repository_test

import (
	"context"
	"testing"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamChannelEntitySoftDeleteRetainsOwnership(t *testing.T) {
	ctx := context.Background()
	db, client := repository.UpstreamChannelsTestDatabase(t)
	repo, ok := repository.NewChannelRepository(db).(service.UpstreamChannelRepository)
	require.True(t, ok)
	channel, err := repo.CreateUpstreamChannel(ctx, service.UpstreamChannelMutation{Code: "delete-fixture", Name: "Delete fixture", Confirm: true})
	require.NoError(t, err)
	var id int64
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('delete-account','gemini','oauth','{}') RETURNING id`).Scan(&id))
	_, err = repo.BindUpstreamChannelAccounts(ctx, service.UpstreamChannelBindingsMutation{ChannelID: channel.ID, Bindings: []service.UpstreamChannelBinding{{AccountID: id}}, Confirm: true})
	require.NoError(t, err)
	before, err := repo.GetUpstreamChannel(ctx, channel.ID)
	require.NoError(t, err)
	require.NoError(t, repository.NewAdminAccountRepository(client, db, nil).Delete(ctx, id))
	bindings, err := repo.ReadUpstreamChannelBindings(ctx, []int64{id})
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	require.Equal(t, channel.ID, bindings[0].ChannelID, "soft-deleted accounts must retain explicit analytics ownership")
	require.True(t, bindings[0].Deleted)
	after, err := repo.GetUpstreamChannel(ctx, channel.ID)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.ErrorIs(t, repo.DeleteUpstreamChannel(ctx, channel.ID, service.UpstreamChannelMutation{Revision: &after.Revision, Confirm: true}), service.ErrUpstreamChannelConflict)
	// Exercise true physical deletion separately, with no usage or billing rows.
	_, err = db.Exec(`DELETE FROM accounts WHERE id=$1`, id)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM upstream_channel_bindings WHERE account_id=$1`, id).Scan(&count))
	require.Zero(t, count)
	final, err := repo.GetUpstreamChannel(ctx, channel.ID)
	require.NoError(t, err)
	require.Equal(t, after.Revision+1, final.Revision)
	require.Zero(t, final.Members)
}
