//go:build integration

package repository

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type astraGroupFixture struct {
	t                   *testing.T
	repo                *accountRepository
	id, selected, other int64
	settings            config.AstraRoutingSettings
}

func newAstraGroupFixture(t *testing.T) *astraGroupFixture {
	t.Helper()
	f := &astraGroupFixture{t: t, repo: newAccountRepositoryWithSQL(testEntClient(t), integrationDB, nil)}
	ctx := t.Context()
	var prior string
	priorErr := integrationDB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='astra_routing_experiment_v1'`).Scan(&prior)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable,credentials,extra) VALUES('astra-group-sync','openai','oauth','active',true,'{"model_mapping":{"gpt-6-astra":"gpt-6-astra","gpt-5":"gpt-5"}}','{}') RETURNING id`).Scan(&f.id))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES('astra-selected','openai') RETURNING id`).Scan(&f.selected))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES('astra-unselected','openai') RETURNING id`).Scan(&f.other))
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id=$1`, f.id)
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, f.id)
		_, _ = integrationDB.Exec(`DELETE FROM groups WHERE id IN ($1,$2)`, f.selected, f.other)
		if priorErr == nil {
			_, _ = integrationDB.Exec(`UPDATE settings SET value=$1 WHERE key='astra_routing_experiment_v1'`, prior)
		} else {
			_, _ = integrationDB.Exec(`DELETE FROM settings WHERE key='astra_routing_experiment_v1'`)
		}
	})
	f.exec(`INSERT INTO account_groups(account_id,group_id,priority,allowed_models) VALUES($1,$2,17,'["gpt-5"]')`, f.id, f.other)
	f.settings = config.AstraRoutingSettings{AccountScheduling: true, SchedulingMode: "groups", SchedulingGroupIDs: []int64{f.selected}, Revision: "auto-groups", CookiePool: config.CodexGatewayPinConfig{Enabled: true, TargetAccountIDs: []int64{f.id}}}
	f.save()
	return f
}

func (f *astraGroupFixture) exec(query string, args ...any) {
	f.t.Helper()
	_, err := integrationDB.ExecContext(f.t.Context(), query, args...)
	require.NoError(f.t, err)
}
func (f *astraGroupFixture) save() {
	f.t.Helper()
	raw, err := json.Marshal(f.settings)
	require.NoError(f.t, err)
	f.exec(`INSERT INTO settings(key,value) VALUES('astra_routing_experiment_v1',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, string(raw))
}
func (f *astraGroupFixture) apply(ready, changed bool) {
	f.t.Helper()
	result, err := f.repo.ApplyAstraScheduling(f.t.Context(), f.id, f.settings, ready)
	require.NoError(f.t, err)
	require.Equal(f.t, changed, result.Changed)
}
func (f *astraGroupFixture) groups() map[int64]qualityGroup {
	f.t.Helper()
	var raw []byte
	require.NoError(f.t, integrationDB.QueryRowContext(f.t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(g)),'[]'::jsonb) FROM account_groups g WHERE account_id=$1`, f.id).Scan(&raw))
	var groups []qualityGroup
	require.NoError(f.t, json.Unmarshal(raw, &groups))
	result := map[int64]qualityGroup{}
	for _, g := range groups {
		result[g.GroupID] = g
	}
	return result
}

func TestAstraGroupsAutomaticallyJoinWithoutPriorRemoval(t *testing.T) {
	f := newAstraGroupFixture(t)
	before := f.groups()
	f.apply(true, true)
	joined := f.groups()
	require.Contains(t, joined, f.selected, "a passing target must join a selected group even without a removal record")
	require.Equal(t, 50, joined[f.selected].Priority)
	require.Equal(t, before[f.other], joined[f.other])
	var payload string
	require.NoError(t, integrationDB.QueryRow(`SELECT payload::text FROM scheduler_outbox WHERE account_id=$1 AND event_type='account_changed' ORDER BY id DESC LIMIT 1`, f.id).Scan(&payload))
	var event struct {
		GroupIDs []int64 `json:"group_ids"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &event))
	require.Contains(t, event.GroupIDs, f.selected, "a newly joined group must be invalidated even when absent from the previous account snapshot")
	f.repo = newAccountRepositoryWithSQL(testEntClient(t), integrationDB, nil)
	f.apply(true, false)
	require.Equal(t, joined, f.groups(), "a new repository instance must retain ownership without rewriting stable memberships")
	f.apply(false, true)
	require.Equal(t, before, f.groups())
	f.apply(false, false)
	f.apply(true, true)
	require.Contains(t, f.groups(), f.selected)
	f.settings.SchedulingMode = "model"
	f.save()
	f.apply(true, true)
	require.Equal(t, before, f.groups(), "leaving group mode removes only memberships added by this feature")
}

func TestAstraGroupsPreserveExistingMembershipsAndLegacyRemoval(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "legacy"}[legacy], func(t *testing.T) {
			f := newAstraGroupFixture(t)
			f.settings.SchedulingGroupIDs = []int64{f.selected, f.other}
			f.save()
			original := f.groups()[f.other]
			if legacy {
				state := astraActionState{Mode: "groups", GroupIDs: f.settings.SchedulingGroupIDs, Removed: []qualityGroup{original}}
				raw, err := json.Marshal(state)
				require.NoError(t, err)
				f.exec(`DELETE FROM account_groups WHERE account_id=$1`, f.id)
				f.exec(`INSERT INTO astra_scheduling_states(account_id,state) VALUES($1,$2)`, f.id, string(raw))
			}
			f.apply(true, true)
			require.Equal(t, original, f.groups()[f.other])
			f.apply(false, true)
			require.Empty(t, f.groups())
			f.apply(true, true)
			require.Equal(t, original, f.groups()[f.other])
			f.settings.SchedulingMode = "account"
			f.save()
			f.apply(true, true)
			require.Equal(t, map[int64]qualityGroup{f.other: original}, f.groups())
		})
	}
}

func TestAstraGroupsSelectionChangeRestoresOwnedActions(t *testing.T) {
	f := newAstraGroupFixture(t)
	original := f.groups()
	f.apply(true, true)
	f.settings.SchedulingGroupIDs = []int64{f.other}
	f.save()
	f.apply(true, true)
	require.Equal(t, original, f.groups(), "unselected auto-added memberships are removed")
	f.apply(false, true)
	f.settings.SchedulingGroupIDs = []int64{f.selected}
	f.save()
	f.apply(false, true)
	require.Equal(t, original, f.groups(), "original memberships are restored before taking over the new selection")
	f.apply(true, true)
	require.Len(t, f.groups(), 2)
}

func TestAstraGroupsManualEditsAreNotOverwritten(t *testing.T) {
	for _, edit := range []string{"priority", "models", "delete", "recreate", "manual_join"} {
		t.Run(edit, func(t *testing.T) {
			f := newAstraGroupFixture(t)
			f.apply(true, true)
			switch edit {
			case "priority":
				f.exec(`UPDATE account_groups SET priority=99 WHERE account_id=$1 AND group_id=$2`, f.id, f.selected)
			case "models":
				f.exec(`UPDATE account_groups SET allowed_models='["gpt-5"]' WHERE account_id=$1 AND group_id=$2`, f.id, f.selected)
			case "delete", "recreate":
				f.exec(`DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.id, f.selected)
				if edit == "recreate" {
					f.exec(`INSERT INTO account_groups(account_id,group_id,created_at) VALUES($1,$2,clock_timestamp()+interval '1 second')`, f.id, f.selected)
				}
			case "manual_join":
				f.apply(false, true)
				f.exec(`INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,99)`, f.id, f.selected)
			}
			manual := f.groups()
			for _, ready := range []bool{true, false} {
				_, err := f.repo.ApplyAstraScheduling(t.Context(), f.id, f.settings, ready)
				require.ErrorContains(t, err, "astra_restore_conflict")
				require.Equal(t, manual, f.groups())
			}
			f.settings.SchedulingMode = "model"
			f.save()
			_, err := f.repo.ApplyAstraScheduling(t.Context(), f.id, f.settings, false)
			require.ErrorContains(t, err, "astra_restore_conflict")
			require.Equal(t, manual, f.groups())
			a, err := f.repo.GetByID(t.Context(), f.id)
			require.NoError(t, err)
			require.True(t, a.IsModelSupported("gpt-6-astra"), "failed mode changes must not apply the new action")
		})
	}
}

func TestAstraGroupsNoopDisableAndUnrelatedEdits(t *testing.T) {
	f := newAstraGroupFixture(t)
	var version time.Time
	require.NoError(t, integrationDB.QueryRow(`SELECT updated_at FROM accounts WHERE id=$1`, f.id).Scan(&version))
	f.apply(false, false)
	f.apply(false, false)
	var after time.Time
	require.NoError(t, integrationDB.QueryRow(`SELECT updated_at FROM accounts WHERE id=$1`, f.id).Scan(&after))
	require.True(t, version.Equal(after), "ownership initialization must not count as a scheduling change")
	var events int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, f.id).Scan(&events))
	require.Zero(t, events)
	f.apply(true, true)
	joined := f.groups()
	f.settings.AccountScheduling = false
	f.save()
	_, err := f.repo.ApplyAstraScheduling(t.Context(), f.id, f.settings, false)
	require.ErrorContains(t, err, "configuration_changed")
	require.Equal(t, joined, f.groups())
	f.settings.AccountScheduling = true
	f.save()
	f.exec(`UPDATE accounts SET name='manual name',updated_at=clock_timestamp() WHERE id=$1`, f.id)
	f.exec(`UPDATE account_groups SET priority=66 WHERE account_id=$1 AND group_id=$2`, f.id, f.other)
	manualOther := f.groups()[f.other]
	f.apply(false, true)
	f.apply(true, true)
	require.Equal(t, manualOther, f.groups()[f.other], "unrelated edits neither block nor get overwritten")
}

func TestAstraGroupsDeletedSelectionRollsBackAndIneligibleNeverJoins(t *testing.T) {
	f := newAstraGroupFixture(t)
	f.settings.SchedulingGroupIDs = []int64{f.selected, f.other}
	f.save()
	f.exec(`UPDATE groups SET deleted_at=NOW() WHERE id=$1`, f.other)
	f.exec(`DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.id, f.other)
	_, err := f.repo.ApplyAstraScheduling(t.Context(), f.id, f.settings, true)
	require.ErrorContains(t, err, "astra_restore_conflict")
	require.Empty(t, f.groups(), "earlier inserts must roll back when another selected group is deleted")
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM astra_scheduling_states WHERE account_id=$1`, f.id).Scan(&count))
	require.Zero(t, count)
	f.settings.SchedulingGroupIDs = []int64{f.selected}
	f.save()
	for _, condition := range []string{"status='error'", "status='active',expires_at=NOW()-interval '1 hour'"} {
		f.exec(`UPDATE accounts SET `+condition+` WHERE id=$1`, f.id)
		result, err := f.repo.ApplyAstraScheduling(t.Context(), f.id, f.settings, true)
		require.NoError(t, err)
		require.False(t, result.Changed)
		require.False(t, result.Allowed)
		require.Empty(t, f.groups())
	}
}
