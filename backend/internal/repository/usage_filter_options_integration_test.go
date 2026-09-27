//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestOwnUsageFilterOptionsIsolation(t *testing.T) {
	ctx := context.Background()
	// Keep every row in a rolled-back transaction. Committed usage logs would
	// leak into the database-wide dashboard totals asserted by UsageLogRepoSuite.
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	suffix := uuid.NewString()
	owner := mustCreateUser(t, client, &service.User{Email: "observer-" + suffix + "@test.com"})
	other := mustCreateUser(t, client, &service.User{Email: "other-" + suffix + "@test.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: owner.ID, Name: "own-key", Key: "sk-" + uuid.NewString()})
	otherKey := mustCreateApiKey(t, client, &service.APIKey{UserID: other.ID, Name: "other-key", Key: "sk-" + uuid.NewString()})
	ownGroup := mustCreateGroup(t, client, &service.Group{Name: "own-group-" + suffix})
	otherGroup := mustCreateGroup(t, client, &service.Group{Name: "other-group-" + suffix})
	shared := mustCreateAccount(t, client, &service.Account{Name: "shared-" + suffix})
	foreign := mustCreateAccount(t, client, &service.Account{Name: "foreign-" + suffix})
	failed := mustCreateAccount(t, client, &service.Account{Name: "failed-" + suffix})
	// The same account serves two users; neither list nor filter choices may
	// infer ownership from account/group management permissions.
	for _, row := range []*service.UsageLog{
		{UserID: owner.ID, APIKeyID: key.ID, AccountID: shared.ID, GroupID: &ownGroup.ID},
		{UserID: other.ID, APIKeyID: otherKey.ID, AccountID: shared.ID, GroupID: &otherGroup.ID},
		{UserID: other.ID, APIKeyID: otherKey.ID, AccountID: foreign.ID, GroupID: &otherGroup.ID},
	} {
		row.RequestID = uuid.NewString()
		row.Model = "model"
		row.ActualCost = 1
		row.CreatedAt = time.Now()
		_, err := repo.Create(ctx, row)
		require.NoError(t, err)
	}
	// Same statement as opsRepository.InsertErrorLog, which only accepts *sql.DB
	// and therefore cannot join the test transaction.
	_, err := tx.ExecContext(ctx, insertOpsErrorLogSQL, opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{
		UserID: &owner.ID, APIKeyID: &key.ID, AccountID: &failed.ID, GroupID: &ownGroup.ID, StatusCode: 502, CreatedAt: time.Now(),
		ErrorPhase: "request", ErrorType: "api_error", Severity: "P1", ErrorSource: "gateway", ErrorOwner: "provider",
	})...)
	require.NoError(t, err)
	for _, kind := range []string{"api_key", "account", "group"} {
		items, err := repo.OwnUsageFilterOptions(ctx, owner.ID, kind, "", false)
		require.NoError(t, err)
		require.Len(t, items, 1)
		ids := map[string]int64{"api_key": key.ID, "account": shared.ID, "group": ownGroup.ID}
		require.Equal(t, ids[kind], items[0].ID)
	}
	items, err := repo.OwnUsageFilterOptions(ctx, owner.ID, "account", "", true)
	require.NoError(t, err)
	require.Len(t, items, 2)
	items, err = repo.OwnUsageFilterOptions(ctx, owner.ID, "account", "failed-", true)
	require.NoError(t, err)
	require.Equal(t, []service.UsageFilterOption{{ID: failed.ID, Name: failed.Name}}, items)
	items, err = repo.OwnUsageFilterOptions(ctx, owner.ID, "account", "' OR 1=1 --", true)
	require.NoError(t, err)
	require.Empty(t, items)
	rows, page, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 100}, usagestats.UsageLogFilters{UserID: owner.ID, AccountID: shared.ID, ExactTotal: true})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), page.Total)
	require.Equal(t, owner.ID, rows[0].UserID)
}
