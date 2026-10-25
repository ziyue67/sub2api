//go:build integration

package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newBudgetFixture(t *testing.T) (*accountTokenGuardV2Repository, *openAIOAuthReauthRepository, int64, service.AccountTokenGuardV2Rules) {
	t.Helper()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "budget-offline-test", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic"}})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	guard := NewAccountTokenGuardV2Repository(integrationDB).(*accountTokenGuardV2Repository)
	require.NoError(t, guard.UpsertAccount(context.Background(), account.ID, true, true))
	_, err := guard.ClaimAccount(context.Background(), account.ID, "budget-owner", time.Hour)
	require.NoError(t, err)
	rules := service.AccountTokenGuardV2Rules{ProbeIntervalSeconds: 180, RetryIntervalSeconds: 120, ReloginCooldownSeconds: 60,
		FailStreakThreshold: 1, ReloginBackoffMaxSeconds: 240, ReloginMaxFailures: 3}
	return guard, NewOpenAIOAuthReauthRepository(integrationDB).(*openAIOAuthReauthRepository), account.ID, rules
}

func finishBudgetTask(t *testing.T, taskID int64, status string, finished time.Time) {
	t.Helper()
	// Only the isolated integration database: no login, request or credentials.
	_, err := integrationDB.ExecContext(context.Background(), `UPDATE openai_oauth_reauth_tasks SET status=$2,stage=$2,finished_at=$3 WHERE id=$1`, taskID, status, finished)
	require.NoError(t, err)
}

func TestReloginBudgetAtomicGatePersistenceAndExplicitResume(t *testing.T) {
	ctx := context.Background()
	guard, tasks, id, rules := newBudgetFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var lastID int64
	for i := 0; i < 3; i++ {
		task, err := tasks.CreateTask(ctx, id, "synthetic-hash") // Also account for explicitly initiated tasks.
		require.NoError(t, err)
		lastID = task.ID
		finished := now.Add(time.Duration(i) * time.Minute)
		finishBudgetTask(t, task.ID, service.OpenAIOAuthReauthStatusFailed, finished)
		stored, _, err := guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, finished, service.AccountTokenGuardV2ProbeAuth)
		require.NoError(t, err)
		require.Equal(t, i+1, stored.ReloginFailStreak)
	}
	// A new repository instance simulates process restart, without in-memory state.
	restarted := NewAccountTokenGuardV2Repository(integrationDB).(*accountTokenGuardV2Repository)
	stored, err := restarted.GetAccount(ctx, id)
	require.NoError(t, err)
	require.True(t, stored.AutoReloginExhausted)
	require.Equal(t, lastID, stored.LastCountedTaskID)
	_, err = restarted.CreateAutomaticRelogin(ctx, id, "synthetic", "budget-owner", rules, now.Add(30*time.Minute))
	require.Equal(t, 409, infraerrors.Code(err))
	stored, _, err = restarted.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now.Add(30*time.Minute), service.AccountTokenGuardV2ProbeOK)
	require.NoError(t, err)
	require.True(t, stored.AutoReloginExhausted, "plain probe OK without a successful re-login cannot reset")
	off := false
	require.NoError(t, restarted.UpdateSwitches(ctx, id, nil, &off))
	stored, err = restarted.GetAccount(ctx, id)
	require.NoError(t, err)
	require.True(t, stored.AutoReloginExhausted, "switch changes must not erase the budget")
	require.Error(t, restarted.ResumeReloginBudget(ctx, id, rules, now), "an active inspection lease blocks recovery")
	require.NoError(t, restarted.CompleteProbe(ctx, id, "budget-owner", service.AccountTokenGuardV2ProbeCompletion{
		ProbeState: service.AccountTokenGuardV2ProbeAuth, LastProbeAt: now, NextProbeAt: now.Add(time.Minute), CooldownUntil: stored.CooldownUntil,
	}))
	require.NoError(t, restarted.ResumeReloginBudget(ctx, id, rules, now.Add(10*time.Minute)))
	stored, err = restarted.GetAccount(ctx, id)
	require.NoError(t, err)
	require.False(t, stored.AutoReloginExhausted)
	require.Zero(t, stored.ReloginFailStreak)
	require.True(t, stored.Enabled)
	require.False(t, stored.AutoReloginEnabled, "explicit recovery does not enable the switch")
	require.Equal(t, now.Add(11*time.Minute), *stored.CooldownUntil)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_oauth_reauth_tasks WHERE account_id=$1`, id).Scan(&count))
	require.Equal(t, 3, count, "restoring a budget must not create a task")
}

func TestReloginBudgetConcurrentObserversAndAutomaticInserts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	guard, tasks, id, rules := newBudgetFixture(t)
	finished := time.Now().UTC().Truncate(time.Microsecond)
	task, err := tasks.CreateTask(ctx, id, "synthetic")
	require.NoError(t, err)
	finishBudgetTask(t, task.ID, service.OpenAIOAuthReauthStatusFailed, finished)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, finished, service.AccountTokenGuardV2ProbeAuth)
			errs <- err
		}()
	}
	wg.Wait()
	for range 2 {
		require.NoError(t, <-errs)
	}
	stored, err := guard.GetAccount(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, stored.ReloginFailStreak)
	_, err = guard.CreateAutomaticRelogin(ctx, id, "synthetic", "another-owner", rules, finished.Add(time.Minute))
	require.Equal(t, 409, infraerrors.Code(err), "a different lease owner cannot queue")
	_, err = guard.CreateAutomaticRelogin(ctx, id, "synthetic", "budget-owner", rules, finished.Add(time.Minute-time.Nanosecond))
	require.Equal(t, 409, infraerrors.Code(err), "just before the boundary is still cooling down")
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := guard.CreateAutomaticRelogin(ctx, id, "synthetic", "budget-owner", rules, finished.Add(time.Minute))
			errs <- err
		}()
	}
	wg.Wait()
	first, second := <-errs, <-errs
	require.True(t, (first == nil) != (second == nil), "exactly one insertion must succeed")
	if first != nil {
		require.Equal(t, 409, infraerrors.Code(first))
	}
	if second != nil {
		require.Equal(t, 409, infraerrors.Code(second))
	}
	require.Error(t, guard.ResumeReloginBudget(ctx, id, rules, finished.Add(2*time.Hour)), "queued work still blocks explicit recovery")
}

func TestReloginBudgetSavedCredentialsMustPassASeparateFreshProbe(t *testing.T) {
	ctx := context.Background()
	guard, tasks, id, rules := newBudgetFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first, err := tasks.CreateTask(ctx, id, "synthetic")
	require.NoError(t, err)
	finishBudgetTask(t, first.ID, service.OpenAIOAuthReauthStatusFailed, now)
	_, _, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now, service.AccountTokenGuardV2ProbeAuth)
	require.NoError(t, err)
	success, err := tasks.CreateTask(ctx, id, "synthetic")
	require.NoError(t, err)
	finishBudgetTask(t, success.ID, service.OpenAIOAuthReauthStatusSucceeded, now.Add(time.Second))
	stored, pending, err := guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now, service.AccountTokenGuardV2ProbeOK)
	require.NoError(t, err)
	require.True(t, pending)
	require.Equal(t, 1, stored.ReloginFailStreak)
	stored, _, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now.Add(2*time.Second), service.AccountTokenGuardV2ProbeAuth)
	require.NoError(t, err)
	require.Equal(t, 2, stored.ReloginFailStreak)
	_, err = guard.CreateAutomaticRelogin(ctx, id, "synthetic", "budget-owner", rules, now.Add(10*time.Second))
	require.Equal(t, 409, infraerrors.Code(err), "a failed post-save verification retains the failure backoff")
	stored, _, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now.Add(3*time.Second), service.AccountTokenGuardV2ProbeOK)
	require.NoError(t, err)
	require.Zero(t, stored.ReloginFailStreak)
	require.Nil(t, stored.CooldownUntil)
	require.False(t, stored.AutoReloginExhausted)
}

func TestReloginBudgetMigrationSeedsOnlyOldTerminalCodexTasks(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE budget_seed_guard(account_id bigint);
        CREATE TEMP TABLE budget_seed_tasks(id bigint,account_id bigint,oauth_profile text,status text);
        CREATE TEMP TABLE budget_seed_rotations(task_id bigint);
        INSERT INTO budget_seed_guard VALUES(1);
        INSERT INTO budget_seed_tasks VALUES(10,1,'codex','failed'),(12,1,'codex','succeeded'),(15,1,'codex','running'),(20,1,'codex','failed'),(30,1,'excel','failed');
        INSERT INTO budget_seed_rotations VALUES(20);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/300_account_relogin_budget.sql")
	require.NoError(t, err)
	sql := strings.NewReplacer("account_token_guard_v2_accounts", "budget_seed_guard", "openai_oauth_reauth_tasks", "budget_seed_tasks", "openai_totp_rotations", "budget_seed_rotations").Replace(string(migration))
	_, err = tx.ExecContext(ctx, sql)
	require.NoError(t, err)
	var marker int64
	var count int
	var exhausted bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT last_counted_task_id,relogin_fail_streak,auto_relogin_exhausted FROM budget_seed_guard`).Scan(&marker, &count, &exhausted))
	require.EqualValues(t, 12, marker)
	require.Zero(t, count)
	require.False(t, exhausted)
}

func TestReloginBudgetPagesAdvancePastSuccessWithoutPrematureReset(t *testing.T) {
	for _, test := range []struct {
		count  int
		last   string
		finish string
	}{
		{100, "failed", ""}, {100, "succeeded", ""}, {101, "failed", ""}, {205, "failed", ""},
		{101, "running", "failed"}, {101, "running", "succeeded"},
	} {
		t.Run(fmt.Sprintf("%d-last-%s-finish-%s", test.count, test.last, test.finish), func(t *testing.T) {
			ctx := context.Background()
			guard, _, id, rules := newBudgetFixture(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			until := now.Add(time.Hour)
			_, err := integrationDB.ExecContext(ctx, `UPDATE account_token_guard_v2_accounts
                SET relogin_fail_streak=2,auto_relogin_exhausted=TRUE,cooldown_until=$2 WHERE account_id=$1`, id, until)
			require.NoError(t, err)
			rows, err := integrationDB.QueryContext(ctx, `WITH seeded AS (
                INSERT INTO openai_oauth_reauth_tasks(account_id,status,stage,expected_credentials_hash,finished_at)
                SELECT $1,CASE WHEN i=$2 THEN $3 ELSE 'succeeded' END,
                  CASE WHEN i=$2 THEN CASE WHEN $3='running' THEN 'starting' ELSE $3 END ELSE 'succeeded' END,
                  'synthetic',CASE WHEN i=$2 AND $3='running' THEN NULL ELSE $4::timestamptz END
                FROM generate_series(1,$2::integer) i ORDER BY i RETURNING id
            ) SELECT id FROM seeded ORDER BY id`, id, test.count, test.last, now)
			require.NoError(t, err)
			var ids []int64
			for rows.Next() {
				var taskID int64
				require.NoError(t, rows.Scan(&taskID))
				ids = append(ids, taskID)
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
			require.Len(t, ids, test.count)
			var stored *service.AccountTokenGuardV2Record
			var pending bool
			var previous int64
			for page := 0; page < 4; page++ {
				stored, pending, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now, service.AccountTokenGuardV2ProbeOK)
				require.NoError(t, err)
				if test.last == "running" && page > 0 {
					require.True(t, pending)
					require.Equal(t, ids[len(ids)-2], stored.LastCountedTaskID)
					require.Equal(t, 2, stored.ReloginFailStreak)
					require.True(t, stored.AutoReloginExhausted)
					finishBudgetTask(t, ids[len(ids)-1], test.finish, now)
					stored, pending, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now, service.AccountTokenGuardV2ProbeOK)
					require.NoError(t, err)
					break
				}
				require.Greater(t, stored.LastCountedTaskID, previous, "a full page beginning with an uncounted success must progress")
				previous = stored.LastCountedTaskID
				if !pending {
					break
				}
				require.Equal(t, 2, stored.ReloginFailStreak, "a partial success page cannot reset prior failures")
				require.True(t, stored.AutoReloginExhausted)
				require.Equal(t, &until, stored.CooldownUntil)
			}
			require.False(t, pending)
			require.Equal(t, ids[len(ids)-1], stored.LastCountedTaskID)
			if test.last == "succeeded" || test.finish == "succeeded" {
				require.Zero(t, stored.ReloginFailStreak)
				require.False(t, stored.AutoReloginExhausted)
				require.Nil(t, stored.CooldownUntil)
			} else {
				require.Equal(t, 3, stored.ReloginFailStreak)
				require.True(t, stored.AutoReloginExhausted)
				require.Equal(t, &until, stored.CooldownUntil)
			}
			again, pending, err := guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now, service.AccountTokenGuardV2ProbeOK)
			require.NoError(t, err)
			require.False(t, pending)
			require.Equal(t, stored.ReloginFailStreak, again.ReloginFailStreak, "cross-page replay cannot count a task twice")
			require.Equal(t, stored.LastCountedTaskID, again.LastCountedTaskID)
		})
	}
}

func TestRestoredReloginCooldownSurvivesPlainOKProbePersistence(t *testing.T) {
	ctx := context.Background()
	guard, tasks, id, rules := newBudgetFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	failed, err := tasks.CreateTask(ctx, id, "synthetic")
	require.NoError(t, err)
	finishBudgetTask(t, failed.ID, service.OpenAIOAuthReauthStatusFailed, now)
	stored, _, err := guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, now, service.AccountTokenGuardV2ProbeAuth)
	require.NoError(t, err)
	require.NoError(t, guard.CompleteProbe(ctx, id, "budget-owner", service.AccountTokenGuardV2ProbeCompletion{
		ProbeState: service.AccountTokenGuardV2ProbeAuth, LastProbeAt: now, NextProbeAt: now.Add(time.Minute), CooldownUntil: stored.CooldownUntil,
	}))
	resumedAt := now.Add(2 * time.Minute)
	require.NoError(t, guard.ResumeReloginBudget(ctx, id, rules, resumedAt))
	until := resumedAt.Add(time.Minute)
	_, err = guard.ClaimAccount(ctx, id, "budget-owner", time.Hour)
	require.NoError(t, err)
	stored, _, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, resumedAt.Add(time.Second), service.AccountTokenGuardV2ProbeOK)
	require.NoError(t, err)
	require.Equal(t, &until, stored.CooldownUntil)
	// Even a stale completion with nil cannot clear the authoritative cooldown.
	require.NoError(t, guard.CompleteProbe(ctx, id, "budget-owner", service.AccountTokenGuardV2ProbeCompletion{
		ProbeState: service.AccountTokenGuardV2ProbeOK, LastProbeAt: resumedAt.Add(time.Second), NextProbeAt: until,
	}))
	stored, err = guard.GetAccount(ctx, id)
	require.NoError(t, err)
	require.Equal(t, &until, stored.CooldownUntil)
	_, err = guard.ClaimAccount(ctx, id, "budget-owner", time.Hour)
	require.NoError(t, err)
	stored, _, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, until.Add(-time.Second), service.AccountTokenGuardV2ProbeAuth)
	require.NoError(t, err)
	require.Equal(t, &until, stored.CooldownUntil)
	_, err = guard.CreateAutomaticRelogin(ctx, id, "synthetic", "budget-owner", rules, until.Add(-time.Second))
	require.Equal(t, 409, infraerrors.Code(err))
	// A genuinely newer succeeded task and a fresh OK still clear normally.
	success, err := tasks.CreateTask(ctx, id, "synthetic")
	require.NoError(t, err)
	finishBudgetTask(t, success.ID, service.OpenAIOAuthReauthStatusSucceeded, until)
	stored, _, err = guard.ReconcileReloginBudget(ctx, id, "budget-owner", rules, until.Add(time.Second), service.AccountTokenGuardV2ProbeOK)
	require.NoError(t, err)
	require.Nil(t, stored.CooldownUntil)
	require.NoError(t, guard.CompleteProbe(ctx, id, "budget-owner", service.AccountTokenGuardV2ProbeCompletion{
		ProbeState: service.AccountTokenGuardV2ProbeOK, LastProbeAt: until.Add(time.Second), NextProbeAt: until.Add(time.Hour),
	}))
	stored, err = guard.GetAccount(ctx, id)
	require.NoError(t, err)
	require.Nil(t, stored.CooldownUntil)
}
