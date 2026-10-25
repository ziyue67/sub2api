//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountOpsSnapshotsPreserveDataAndVisibility(t *testing.T) {
	ctx := context.Background()
	client := testEntTx(t).Client()
	repo := &accountRepository{client: client}
	end := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	credentials := map[string]any{"base_url": "https://fixture.invalid", "api_key": "fixture-only"}
	extra := map[string]any{"passive_usage_sampled_at": end.Add(-time.Hour).Format(time.RFC3339), "session_window_utilization": 0.9}
	visible, err := client.Account.Create().SetName("monitor-visible").SetPlatform("anthropic").SetType("oauth").SetCredentials(credentials).SetExtra(extra).SetSessionWindowEnd(end).Save(ctx)
	require.NoError(t, err)
	hidden, err := client.Account.Create().SetName("monitor-hidden").SetPlatform("openai").SetType("apikey").Save(ctx)
	require.NoError(t, err)
	deleted, err := client.Account.Create().SetName("monitor-deleted").SetPlatform("openai").SetType("apikey").SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().SetName("monitor-observer-scope").SetPlatform("anthropic").Save(ctx)
	require.NoError(t, err)
	_, err = client.AccountGroup.Create().SetAccountID(visible.ID).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	ids := []int64{hidden.ID, deleted.ID, visible.ID, visible.ID}
	rows, err := repo.ListAccountOpsSnapshots(ctx, ids)
	require.NoError(t, err)
	require.Len(t, rows, 2, "deduplicate IDs and exclude soft-deleted accounts")
	rows, err = repo.ListAccountOpsSnapshots(service.WithObserverScope(ctx, []int64{group.ID}), ids)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	account := rows[0]
	require.Equal(t, visible.ID, account.ID)
	require.Equal(t, "monitor-visible", account.Name)
	require.Equal(t, "anthropic", account.Platform)
	require.Equal(t, "oauth", account.Type)
	require.Equal(t, credentials, account.Credentials, "identity hash needs all credentials")
	require.Equal(t, extra, account.Extra)
	require.Equal(t, end, *account.SessionWindowEnd)
	require.Empty(t, account.Groups, "routing groups are filtered but not hydrated")
	require.Nil(t, account.Proxy)
	rows, err = repo.ListAccountOpsSnapshots(service.WithObserverScope(ctx, nil), nil)
	require.NoError(t, err)
	require.Empty(t, rows, "an observer with no grants must not see accounts")
	rows, err = repo.ListAccountOpsSnapshots(ctx, []int64{})
	require.NoError(t, err)
	require.Empty(t, rows, "empty IDs must not become an all-accounts scan")
	rows, err = repo.ListAccountOpsSnapshots(service.WithObserverScope(ctx, []int64{group.ID}), nil)
	require.NoError(t, err)
	require.Len(t, rows, 1, "all-accounts snapshots retain observer scope")
}
