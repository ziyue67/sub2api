//go:build integration

package repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Exercise the service with the real account queries. In-memory repositories
// previously returned error accounts even for the active-only scheduling query.
func TestTokenGuardRecoveryRealRepositoryScope(t *testing.T) {
	for _, selectedGroups := range []bool{false, true} {
		name := "all_groups"
		if selectedGroups {
			name = "selected_groups"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tx := testEntTx(t)
			client := tx.Client()
			accounts := newAccountRepositoryWithSQL(client, tx, nil)
			guardRepo := NewAccountTokenGuardRepository(integrationDB)
			group1 := mustCreateGroup(t, client, &service.Group{Name: "guard-one"})
			group2 := mustCreateGroup(t, client, &service.Group{Name: "guard-two"})
			create := func(name, status string) *service.Account {
				return mustCreateAccount(t, client, &service.Account{Name: name, Platform: service.PlatformOpenAI,
					Type: service.AccountTypeOAuth, Status: status, Credentials: map[string]any{"access_token": "test-access"}})
			}
			active := create("active@example.com", service.StatusActive)
			// The admin "active" filter also filters scheduling/cooldowns;
			// these must not narrow credential inspection.
			err := client.Account.UpdateOneID(active.ID).SetSchedulable(false).
				SetRateLimitResetAt(time.Now().Add(time.Hour)).Exec(ctx)
			require.NoError(t, err)
			failed := create("error@example.com", service.StatusError)
			disabled := create("disabled@example.com", service.StatusDisabled)
			deleted := create("deleted@example.com", service.StatusError)
			require.NoError(t, client.Account.UpdateOneID(deleted.ID).SetDeletedAt(time.Now()).Exec(ctx))
			outside := create("outside@example.com", service.StatusError)
			managed := create("operations@example.com", service.StatusError)
			_, err = tx.ExecContext(ctx, "INSERT INTO account_token_guard_v2_accounts(account_id,enabled,auto_relogin_enabled) VALUES($1,false,true)", managed.ID)
			require.NoError(t, err)
			mustBindAccountToGroup(t, client, managed.ID, group1.ID, 1)
			for _, account := range []*service.Account{active, failed, disabled, deleted} {
				mustBindAccountToGroup(t, client, account.ID, group1.ID, 1)
				mustBindAccountToGroup(t, client, account.ID, group2.ID, 1)
			}
			ids := []int64{active.ID, failed.ID, disabled.ID, deleted.ID, outside.ID, managed.ID}
			t.Cleanup(func() {
				_, cleanupErr := integrationDB.ExecContext(context.Background(), "DELETE FROM account_token_guard_states WHERE account_id = ANY($1)", pq.Array(ids))
				require.NoError(t, cleanupErr)
				_, cleanupErr = integrationDB.ExecContext(context.Background(), "DELETE FROM account_token_guard_events WHERE account_id = ANY($1)", pq.Array(ids))
				require.NoError(t, cleanupErr)
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()
			svc := service.NewAccountTokenGuardService(NewSettingRepository(client), guardRepo, accounts, nil, nil)
			cfg := service.AccountTokenGuardConfig{IntervalSeconds: 300, ProbeTimeoutSeconds: 5, ProbeConcurrency: 1,
				MaxProbePerCycle: 100, FailStreakThreshold: 2, ProbeEndpoint: server.URL, AutoRelogin: false}
			want := []int64{active.ID, failed.ID, outside.ID}
			if selectedGroups {
				cfg.GroupIDs = []int64{group1.ID, group2.ID}
				want = []int64{active.ID, failed.ID}
			}
			_, err = svc.SaveConfig(ctx, cfg)
			require.NoError(t, err)
			stats, err := svc.RunCycle(ctx, false)
			require.NoError(t, err)
			require.Equal(t, len(want), stats.Probed)
			require.Equal(t, len(want), stats.Transient)
			states, err := guardRepo.ListStates(ctx)
			require.NoError(t, err)
			var got []int64
			for _, state := range states {
				for _, id := range ids {
					if state.AccountID == id {
						got = append(got, id)
					}
				}
			}
			require.ElementsMatch(t, want, got)
		})
	}
}
