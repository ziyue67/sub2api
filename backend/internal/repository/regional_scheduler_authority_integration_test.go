//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRegionalAuthoritySerializesAndRestoresRevision(t *testing.T) {
	ctx := context.Background()
	region := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM regional_scheduler_streams WHERE region_id=$1", region)
	}()
	authority := NewRegionalSchedulerAuthority(integrationDB)
	_, missingErr := authority.Restore(ctx, region, false)
	require.Error(t, missingErr)
	_, errInit := authority.Restore(ctx, region, true)
	require.NoError(t, errInit)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := authority.Publish(ctx, region, func(_ context.Context, revision uint64, _ service.AccountRepository, _ service.GroupRepository) ([]byte, error) {
			close(entered)
			<-release
			return []byte(fmt.Sprintf("packet-%d", revision)), nil
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not start")
	}
	_, err := authority.Publish(ctx, region, func(context.Context, uint64, service.AccountRepository, service.GroupRepository) ([]byte, error) {
		return nil, errors.New("contender must not enter exporter")
	})
	require.ErrorContains(t, err, "publisher busy")
	close(release)
	require.NoError(t, <-done)
	restored := NewRegionalSchedulerAuthority(integrationDB)
	wire, err := restored.Restore(ctx, region, false)
	require.NoError(t, err)
	require.Equal(t, []byte("packet-1"), wire)
	// An export failure cannot consume a visible revision or replace the packet.
	_, err = restored.Publish(ctx, region, func(context.Context, uint64, service.AccountRepository, service.GroupRepository) ([]byte, error) {
		return nil, errors.New("export failed")
	})
	require.Error(t, err)
	wire, err = restored.Publish(ctx, region, func(_ context.Context, revision uint64, _ service.AccountRepository, _ service.GroupRepository) ([]byte, error) {
		require.Equal(t, uint64(2), revision)
		return []byte("packet-2"), nil
	})
	require.NoError(t, err)
	require.Equal(t, []byte("packet-2"), wire)
	wire, err = restored.Restore(ctx, region, false)
	require.NoError(t, err)
	require.Equal(t, []byte("packet-2"), wire)
}

func TestRegionalAuthorityUsesOneSnapshotAcrossBucketsAndEdges(t *testing.T) {
	ctx := context.Background()
	region := fmt.Sprintf("test-%d", time.Now().UnixNano())
	var groupID, accountID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "INSERT INTO groups(name,platform) VALUES('regional-before','openai') RETURNING id").Scan(&groupID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('regional','openai','apikey','{"api_key":"before"}','active',true) RETURNING id`).Scan(&accountID))
	_, err := integrationDB.ExecContext(ctx, "INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)", accountID, groupID)
	require.NoError(t, err)
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM regional_scheduler_streams WHERE region_id=$1", region)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM account_groups WHERE account_id=$1", accountID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", accountID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id=$1", groupID)
	}()
	authority := NewRegionalSchedulerAuthority(integrationDB)
	_, errInit := authority.Restore(ctx, region, true)
	require.NoError(t, errInit)
	_, err = authority.Publish(ctx, region, func(ctx context.Context, _ uint64, accounts service.AccountRepository, groups service.GroupRepository) ([]byte, error) {
		before, err := accounts.ListSchedulableByGroupIDAndPlatform(ctx, groupID, "openai")
		if err != nil {
			return nil, err
		}
		require.Len(t, before, 1)
		require.Equal(t, "before", before[0].Credentials["api_key"])
		// A different transaction commits membership, credentials and group changes
		// between bucket queries. The exporter must continue seeing the old view.
		tx, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials='{"api_key":"after"}' WHERE id=$1`, accountID)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "UPDATE groups SET name='regional-after' WHERE id=$1", groupID)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "DELETE FROM account_groups WHERE account_id=$1", accountID)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		after, err := accounts.ListSchedulableByGroupIDAndPlatforms(ctx, groupID, []string{"openai", "antigravity"})
		if err != nil {
			return nil, err
		}
		require.Len(t, after, 1)
		require.Equal(t, "before", after[0].Credentials["api_key"])
		require.Contains(t, after[0].GroupIDs, groupID)
		group, err := groups.GetByIDLite(ctx, groupID)
		if err != nil {
			return nil, err
		}
		require.Equal(t, "regional-before", group.Name)
		// Raw SQL must be scoped too, rather than accidentally using the pool.
		repo := accounts.(*accountRepository)
		var name string
		rows, err := repo.sql.QueryContext(ctx, "SELECT name FROM groups WHERE id=$1", groupID)
		require.NoError(t, err)
		require.True(t, rows.Next())
		require.NoError(t, rows.Scan(&name))
		require.NoError(t, rows.Close())
		require.Equal(t, "regional-before", name)
		return []byte("consistent"), nil
	})
	require.NoError(t, err)
	// The next publication must capture the newly committed removal.
	_, err = authority.Publish(ctx, region, func(ctx context.Context, revision uint64, accounts service.AccountRepository, groups service.GroupRepository) ([]byte, error) {
		require.Equal(t, uint64(2), revision)
		current, err := accounts.ListSchedulableByGroupIDAndPlatform(ctx, groupID, "openai")
		if err != nil {
			return nil, err
		}
		require.Empty(t, current)
		group, err := groups.GetByIDLite(ctx, groupID)
		if err != nil {
			return nil, err
		}
		require.Equal(t, "regional-after", group.Name)
		return []byte("removed"), nil
	})
	require.NoError(t, err)
}
