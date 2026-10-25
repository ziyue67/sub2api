//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityOwnershipReconciliationPreservesLiveRestrictionsAndMatchingOwnership(t *testing.T) {
	ctx := context.Background()
	f, _, _ := newQualityIsolationFixture(t)
	require.Equal(t, "groups_removed", isolationRound(f, "failed", "failed"))
	// Another administrator removed one live restriction, leaving its old owner.
	f.exec(`UPDATE accounts SET extra=extra #- '{quality_model_isolation,sol}',updated_at=clock_timestamp() WHERE id=$1`)
	repo := f.plans.(service.QualityOwnershipRepository)
	preview, err := repo.QualityOwnership(ctx, f.plan.ID, nil)
	require.NoError(t, err)
	require.Equal(t, 1, preview.RelinquishableCount)
	var before string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT row_to_json(a)::text FROM accounts a WHERE id=$1`, f.account).Scan(&before))
	view, err := repo.QualityOwnership(ctx, f.plan.ID, &service.QualityOwnershipRequest{Fence: preview.Fence, Confirm: true})
	require.NoError(t, err)
	require.Empty(t, view.Fence)
	var after, state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT row_to_json(a)::text FROM accounts a WHERE id=$1`, f.account).Scan(&after))
	require.JSONEq(t, before, after)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&state))
	require.Contains(t, state, "astra")
	require.NotContains(t, state, `"sol":`)
	// Original scope still owns a matching model and removed groups.
	plan, err := f.plans.GetByID(ctx, f.plan.ID)
	require.NoError(t, err)
	plan.PelicanConfig.ModelIDs = []string{"astra"}
	_, err = f.plans.Update(ctx, plan)
	require.Equal(t, service.QualityScopeConflictReason, infraerrors.Reason(err))
	require.Equal(t, "stale_run", isolationRound(f, "passed", "passed"), "a running old round cannot republish pre-reconciliation state")
}
func TestQualityOwnershipConfirmationRejectsConcurrentRuleAccountAndStateChanges(t *testing.T) {
	for _, change := range []string{"rule", "account", "state", "entry"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			f, _, _ := newQualityIsolationFixture(t)
			isolationRound(f, "failed", "passed")
			f.exec(`UPDATE accounts SET extra=extra #- '{quality_model_isolation,sol}' WHERE id=$1`)
			repo := f.plans.(service.QualityOwnershipRepository)
			p, err := repo.QualityOwnership(ctx, f.plan.ID, nil)
			require.NoError(t, err)
			switch change {
			case "rule":
				_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_plans SET updated_at=clock_timestamp() WHERE id=$1`, f.plan.ID)
			case "account":
				f.exec(`UPDATE accounts SET updated_at=clock_timestamp() WHERE id=$1`)
			case "state":
				_, err = integrationDB.ExecContext(ctx, `UPDATE account_quality_states SET state=jsonb_set(state,'{quality_5xx_episode}','123') WHERE plan_id=$1`, f.plan.ID)
			case "entry":
				f.exec(`UPDATE accounts SET extra=jsonb_set(extra,'{quality_model_isolation,sol}','{"reason":"quality_rule:999"}') WHERE id=$1`)
			}
			require.NoError(t, err)
			_, err = repo.QualityOwnership(ctx, f.plan.ID, &service.QualityOwnershipRequest{Fence: p.Fence, Confirm: true})
			require.Equal(t, "QUALITY_OWNERSHIP_PREVIEW_CHANGED", infraerrors.Reason(err))
		})
	}
}

func TestQualityOwnershipExternalMarkerIsKeptOnAccountAfterRelinquishingSnapshot(t *testing.T) {
	ctx := context.Background()
	f, _, _ := newQualityIsolationFixture(t)
	isolationRound(f, "failed", "passed")
	f.exec(`UPDATE accounts SET extra=jsonb_set(extra,'{quality_model_isolation,sol}', '{"reason":"quality_rule:999","isolated_at":"2026-01-01T00:00:00Z"}'),updated_at=clock_timestamp() WHERE id=$1`)
	repo := f.plans.(service.QualityOwnershipRepository)
	p, err := repo.QualityOwnership(ctx, f.plan.ID, nil)
	require.NoError(t, err)
	require.Equal(t, 1, p.RelinquishableCount)
	_, err = repo.QualityOwnership(ctx, f.plan.ID, &service.QualityOwnershipRequest{Confirm: true, Fence: p.Fence})
	require.NoError(t, err)
	var reason string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra->'quality_model_isolation'->'sol'->>'reason' FROM accounts WHERE id=$1`, f.account).Scan(&reason))
	require.Equal(t, "quality_rule:999", reason)
	var remaining int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&remaining))
	require.Zero(t, remaining)
	// A later normal failed round still cannot take over the external entry.
	require.NoError(t, f.plans.FinishPelican(ctx, f.plan.ID, f.until, time.Now()))
	f.claim()
	require.Equal(t, "action_conflict", isolationRound(f, "failed", "passed"))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra->'quality_model_isolation'->'sol'->>'reason' FROM accounts WHERE id=$1`, f.account).Scan(&reason))
	require.Equal(t, "quality_rule:999", reason)
}

func TestQualityOwnershipSameMarkerChangeCannotBeSilentlyRelinquished(t *testing.T) {
	ctx := context.Background()
	f, _, _ := newQualityIsolationFixture(t)
	isolationRound(f, "failed", "passed")
	f.exec(`UPDATE accounts SET extra=jsonb_set(extra,'{quality_model_isolation,sol,isolated_at}','"changed-by-administrator"'),updated_at=clock_timestamp() WHERE id=$1`)
	repo := f.plans.(service.QualityOwnershipRepository)
	p, err := repo.QualityOwnership(ctx, f.plan.ID, nil)
	require.NoError(t, err)
	require.Zero(t, p.RelinquishableCount)
	require.Equal(t, "changed_same_owner", p.Entries[0].Status)
	var before string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&before))
	_, err = repo.QualityOwnership(ctx, f.plan.ID, &service.QualityOwnershipRequest{Confirm: true, Fence: p.Fence})
	require.NoError(t, err)
	var after string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&after))
	require.JSONEq(t, before, after)
}
func TestQualityOwnershipConcurrentConfirmationsCommitOnlyOneAndRejectOldRound(t *testing.T) {
	ctx := context.Background()
	f, _, _ := newQualityIsolationFixture(t)
	isolationRound(f, "failed", "passed")
	f.exec(`UPDATE accounts SET extra=extra #- '{quality_model_isolation,sol}',updated_at=clock_timestamp() WHERE id=$1`)
	repo := f.plans.(service.QualityOwnershipRepository)
	p, err := repo.QualityOwnership(ctx, f.plan.ID, nil)
	require.NoError(t, err)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			_, e := repo.QualityOwnership(ctx, f.plan.ID, &service.QualityOwnershipRequest{Confirm: true, Fence: p.Fence})
			errors <- e
		}()
	}
	successes, conflicts := 0, 0
	for range 2 {
		if e := <-errors; e == nil {
			successes++
		} else {
			require.Equal(t, "QUALITY_OWNERSHIP_PREVIEW_CHANGED", infraerrors.Reason(e))
			conflicts++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	require.Equal(t, "stale_run", isolationRound(f, "passed", "passed"))
}
func TestQualityOwnershipGroupPresenceDoesNotDiscardMatchingModelOwnership(t *testing.T) {
	ctx := context.Background()
	f, group, _ := newQualityIsolationFixture(t)
	isolationRound(f, "failed", "failed")
	f.exec(`INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,99)`, group)
	repo := f.plans.(service.QualityOwnershipRepository)
	p, err := repo.QualityOwnership(ctx, f.plan.ID, nil)
	require.NoError(t, err)
	require.Zero(t, p.RelinquishableCount)
	var before string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&before))
	_, err = repo.QualityOwnership(ctx, f.plan.ID, &service.QualityOwnershipRequest{Confirm: true, Fence: p.Fence})
	require.NoError(t, err)
	var after string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.plan.ID).Scan(&after))
	require.JSONEq(t, before, after)
	require.Equal(t, 1, f.count(`SELECT count(*) FROM account_groups WHERE account_id=$1 AND priority=99`))
}
