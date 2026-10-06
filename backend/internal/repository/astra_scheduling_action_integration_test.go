//go:build integration

package repository

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAstraSchedulingModesOwnedRestoration(t *testing.T) {
	ctx := t.Context()
	var prior string
	priorErr := integrationDB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='astra_routing_experiment_v1'`).Scan(&prior)
	t.Cleanup(func() {
		if priorErr == nil {
			_, _ = integrationDB.ExecContext(ctx, `UPDATE settings SET value=$1 WHERE key='astra_routing_experiment_v1'`, prior)
		} else {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key='astra_routing_experiment_v1'`)
		}
	})
	repo := newAccountRepositoryWithSQL(testEntClient(t), integrationDB, nil)
	for _, mode := range []string{"account", "model", "groups"} {
		t.Run(mode, func(t *testing.T) {
			var id, gid, other int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable,credentials,extra) VALUES('astra-mode','openai','oauth','active',true,'{"access_token":"keep","model_mapping":{"gpt-6-astra":"gpt-6-astra","gpt-5":"gpt-5"}}','{}') RETURNING id`).Scan(&id))
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES('astra-mode','openai') RETURNING id`).Scan(&gid))
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES('astra-other','openai') RETURNING id`).Scan(&other))
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, id)
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id IN ($1,$2)`, gid, other)
			})
			_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority,allowed_models) VALUES($1,$2,7,'["gpt-6-astra"]'),($1,$3,50,NULL)`, id, gid, other)
			require.NoError(t, err)
			s := config.AstraRoutingSettings{AccountScheduling: true, SchedulingMode: mode, SchedulingGroupIDs: []int64{gid}, Revision: "test", CookiePool: config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{id}}}
			save := func() {
				raw, e := json.Marshal(s)
				require.NoError(t, e)
				_, e = integrationDB.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('astra_routing_experiment_v1',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, string(raw))
				require.NoError(t, e)
			}
			save()
			result, err := repo.ApplyAstraScheduling(ctx, id, s, false)
			require.NoError(t, err)
			require.True(t, result.Changed)
			result, err = repo.ApplyAstraScheduling(ctx, id, s, false)
			require.NoError(t, err)
			require.False(t, result.Changed)
			a, err := repo.GetByID(ctx, id)
			require.NoError(t, err)
			require.Equal(t, mode != "account", a.Schedulable)
			if mode == "model" {
				require.False(t, a.IsModelSupported("gpt-6-astra"))
				require.True(t, a.IsModelSupported("gpt-5"))
				require.Equal(t, "keep", a.GetCredential("access_token"))
			}
			if mode == "groups" {
				require.Equal(t, []int64{other}, a.GroupIDs)
			}
			result, err = repo.ApplyAstraScheduling(ctx, id, s, true)
			require.NoError(t, err)
			require.True(t, result.Changed)
			a, err = repo.GetByID(ctx, id)
			require.NoError(t, err)
			require.True(t, a.Schedulable)
			require.True(t, a.IsModelSupported("gpt-6-astra"))
			var priority int
			var models string
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT priority,allowed_models::text FROM account_groups WHERE account_id=$1 AND group_id=$2`, id, gid).Scan(&priority, &models))
			require.Equal(t, 7, priority)
			require.JSONEq(t, `["gpt-6-astra"]`, models)
			_, err = repo.ApplyAstraScheduling(ctx, id, s, false)
			require.NoError(t, err)
			s.SchedulingMode = "model"
			if mode == "model" {
				s.SchedulingMode = "groups"
			}
			save()
			result, err = repo.ApplyAstraScheduling(ctx, id, s, false)
			require.NoError(t, err)
			require.True(t, result.Changed)
			_, err = repo.ApplyAstraScheduling(ctx, id, s, true)
			require.NoError(t, err)
			s.SchedulingMode = mode
			save()
			_, err = repo.ApplyAstraScheduling(ctx, id, s, false)
			require.NoError(t, err)
			switch mode {
			case "account":
				_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET updated_at=clock_timestamp(),name='manual' WHERE id=$1`, id)
			case "model":
				_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}','{"gpt-5":"manual"}'::jsonb) WHERE id=$1`, id)
			case "groups":
				_, err = integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,99)`, id, gid)
			}
			require.NoError(t, err)
			_, err = repo.ApplyAstraScheduling(ctx, id, s, true)
			require.ErrorContains(t, err, "astra_restore_conflict")
			s.Revision = "stale"
			_, err = repo.ApplyAstraScheduling(ctx, id, s, false)
			require.ErrorContains(t, err, "configuration_changed")
			s.AccountScheduling = false
			save()
			_, err = repo.ApplyAstraScheduling(ctx, id, s, true)
			require.ErrorContains(t, err, "configuration_changed")
		})
	}
}
