package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageHistoryRepositoryTenantPredicates(t *testing.T) {
	where, args := imageArchiveWhere(service.ImageArchiveFilter{UserID: 12, APIKeyID: 34})
	require.Equal(t, " WHERE a.expires_at > NOW() AND a.user_id = $1 AND a.api_key_id = $2", where)
	require.Equal(t, []any{int64(12), int64(34)}, args)
}

func TestImageHistoryRepositoryBillingStatesAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, state, want     string
		user, key             int64
		request               string
		balance, subscription float64
	}{
		{"applied", "applied", "charged", 12, 34, "client:one", 0.12, 0},
		{"complete_subscription", "complete", "charged", 12, 34, "client:one", 0, 0.23},
		{"free", "complete", "no_charge", 12, 34, "client:one", 0, 0},
		{"pending", "pending", "pending", 12, 34, "client:one", 0.12, 0},
		{"legacy", "legacy", "unknown", 12, 34, "client:one", 0.12, 0},
		{"blocked", "blocked", "unknown", 12, 34, "client:one", 0.12, 0},
		{"wrong_user", "complete", "unknown", 99, 34, "client:one", 0.12, 0},
		{"wrong_key", "complete", "unknown", 12, 99, "client:one", 0.12, 0},
		{"wrong_request", "complete", "unknown", 12, 34, "client:other", 0.12, 0},
		{"negative", "complete", "unknown", 12, 34, "client:one", -0.12, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := &imageArchiveRepository{db: db}
			event := service.UsageBillingEvent{Version: 1, Command: service.UsageBillingCommand{UserID: tc.user, APIKeyID: tc.key, RequestID: tc.request, BalanceCost: tc.balance, SubscriptionCost: tc.subscription}}
			payload, err := json.Marshal(event)
			require.NoError(t, err)
			mock.ExpectQuery(`SELECT state,payload FROM usage_billing_events`).WithArgs("client:one", int64(34), int64(12)).WillReturnRows(sqlmock.NewRows([]string{"state", "payload"}).AddRow(tc.state, payload))
			got, err := repo.ImageHistoryBilling(context.Background(), 12, 34, "client:one")
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Status)
			if tc.want == "charged" || tc.want == "no_charge" {
				require.NotNil(t, got.Amount)
				require.InDelta(t, tc.balance+tc.subscription, *got.Amount, 1e-9)
			} else {
				require.Nil(t, got.Amount)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
