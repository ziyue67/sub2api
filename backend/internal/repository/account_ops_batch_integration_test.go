//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountOpsBatchConfigAndOutboxRollbackTogether(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(integrationEntClient)
	const key = "account_ops_notifications_v1"
	previous, previousErr := settings.GetValue(ctx, key)
	t.Cleanup(func() {
		if previousErr == nil {
			_ = settings.Set(ctx, key, previous)
		} else {
			_ = settings.Delete(ctx, key)
		}
	})
	ids := make([]int64, 2)
	for i := range ids {
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('batch-rollback-fixture','openai','apikey','active',true) RETURNING id`).Scan(&ids[i]))
		defer cleanupOnce(t, ids[i])
	}
	repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
	svc := service.NewAccountOpsService(settings, repo, nil)
	svc.SetNotificationDependencies(NewAccountRepository(integrationEntClient, integrationDB, nil), nil, false, "UTC")
	on := true
	cfg := service.AccountOpsConfig{Enabled: true, Recipient: "ops@example.test", CooldownMinutes: 60}
	for _, id := range ids {
		cfg.BalanceThresholds = append(cfg.BalanceThresholds, service.AccountOpsBalanceRule{AccountID: id, Enabled: true, Threshold: 5, Unit: "USD", NotifyAlert: &on, NotifyRecovery: &on})
	}
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	for _, id := range ids {
		criteria, _, _, _ := cfg.ThresholdCriteria(id, "balance_threshold")
		require.NoError(t, repo.ObserveThreshold(ctx, service.AccountOpsEvent{AccountID: id, Kind: "balance_threshold", Criteria: criteria}, "active", time.Now(), true, true))
	}
	before, err := settings.GetValue(ctx, key)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION batch_fail_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic batch outbox failure'; END$$;CREATE TRIGGER batch_fail_update BEFORE UPDATE ON account_ops_threshold_events FOR EACH ROW WHEN (OLD.account_id=%d) EXECUTE FUNCTION batch_fail_update()`, ids[1]))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DROP TRIGGER IF EXISTS batch_fail_update ON account_ops_threshold_events;DROP FUNCTION IF EXISTS batch_fail_update()`)
	})
	threshold := 10.0
	rule := service.AccountOpsRuleUpdate{Metric: "balance", Enabled: true, Threshold: &threshold, Unit: "USD", NotifyAlert: &on, NotifyRecovery: &on}
	_, err = svc.SaveRulesBatch(ctx, ids, rule)
	require.Error(t, err)
	after, err := settings.GetValue(ctx, key)
	require.NoError(t, err)
	require.Equal(t, before, after)
	for _, id := range ids {
		var state string
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_threshold_events WHERE account_id=$1`, id).Scan(&state))
		require.Equal(t, "pending", state)
	}
	_, err = integrationDB.ExecContext(ctx, `DROP TRIGGER batch_fail_update ON account_ops_threshold_events;DROP FUNCTION batch_fail_update()`)
	require.NoError(t, err)
	out, err := svc.SaveRulesBatch(ctx, ids, rule)
	require.NoError(t, err)
	for _, r := range out.BalanceThresholds {
		require.Equal(t, 10.0, r.Threshold)
	}
	for _, id := range ids {
		var state string
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_threshold_events WHERE account_id=$1`, id).Scan(&state))
		require.Equal(t, "suppressed", state)
	}
}

func TestAccountOpsGroupedBatchRollsBackBothMetricsAndOutbox(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(integrationEntClient)
	const key = "account_ops_notifications_v1"
	previous, previousErr := settings.GetValue(ctx, key)
	t.Cleanup(func() {
		if previousErr == nil {
			_ = settings.Set(ctx, key, previous)
		} else {
			_ = settings.Delete(ctx, key)
		}
	})
	ids := make([]int64, 2)
	for i, kind := range []string{"apikey", "oauth"} {
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('grouped-rollback-fixture','openai',$1,'active',true) RETURNING id`, kind).Scan(&ids[i]))
		defer cleanupOnce(t, ids[i])
	}
	repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
	svc := service.NewAccountOpsService(settings, repo, nil)
	svc.SetNotificationDependencies(NewAccountRepository(integrationEntClient, integrationDB, nil), nil, false, "UTC")
	on, off := true, false
	cfg := service.AccountOpsConfig{Enabled: true, Recipient: "ops@example.test", CooldownMinutes: 60,
		BalanceThresholds: []service.AccountOpsBalanceRule{{AccountID: ids[0], Enabled: true, Threshold: 5, Unit: "USD", NotifyAlert: &on, NotifyRecovery: &on}},
		QuotaThresholds:   []service.AccountOpsQuotaRule{{AccountID: ids[1], Enabled: true, ThresholdPercent: 75, Window: "any", NotifyAlert: &on, NotifyRecovery: &on}},
	}
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	for i, kind := range []string{"balance_threshold", "quota_threshold"} {
		criteria, _, _, _ := cfg.ThresholdCriteria(ids[i], kind)
		require.NoError(t, repo.ObserveThreshold(ctx, service.AccountOpsEvent{AccountID: ids[i], Kind: kind, Criteria: criteria}, "active", time.Now(), true, true))
	}
	before, err := settings.GetValue(ctx, key)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION grouped_batch_fail_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic second-group outbox failure'; END$$;CREATE TRIGGER grouped_batch_fail_update BEFORE UPDATE ON account_ops_threshold_events FOR EACH ROW WHEN (OLD.account_id=%d) EXECUTE FUNCTION grouped_batch_fail_update()`, ids[1]))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DROP TRIGGER IF EXISTS grouped_batch_fail_update ON account_ops_threshold_events;DROP FUNCTION IF EXISTS grouped_batch_fail_update()`)
	})
	amount, percent := 10.0, 90.0
	groups := []service.AccountOpsRuleGroup{
		{AccountIDs: ids[:1], Rule: service.AccountOpsRuleUpdate{Metric: "balance", Enabled: true, Threshold: &amount, Unit: "USD", NotifyAlert: &on, NotifyRecovery: &off}},
		{AccountIDs: ids[1:], Rule: service.AccountOpsRuleUpdate{Metric: "quota", Enabled: true, ThresholdPercent: &percent, Window: "any", NotifyAlert: &off, NotifyRecovery: &on}},
	}
	_, err = svc.SaveRuleGroupsBatch(ctx, groups)
	require.Error(t, err)
	after, err := settings.GetValue(ctx, key)
	require.NoError(t, err)
	require.Equal(t, before, after, "failure in OAuth outbox must roll back the API key group too")
	for _, id := range ids {
		var state string
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_threshold_events WHERE account_id=$1`, id).Scan(&state))
		require.Equal(t, "pending", state)
	}
	_, err = integrationDB.ExecContext(ctx, `DROP TRIGGER grouped_batch_fail_update ON account_ops_threshold_events;DROP FUNCTION grouped_batch_fail_update()`)
	require.NoError(t, err)
	out, err := svc.SaveRuleGroupsBatch(ctx, groups)
	require.NoError(t, err)
	require.Equal(t, 10.0, out.BalanceThresholds[0].Threshold)
	require.True(t, *out.BalanceThresholds[0].NotifyAlert)
	require.False(t, *out.BalanceThresholds[0].NotifyRecovery)
	require.Equal(t, 90.0, out.QuotaThresholds[0].ThresholdPercent)
	require.False(t, *out.QuotaThresholds[0].NotifyAlert)
	require.True(t, *out.QuotaThresholds[0].NotifyRecovery)
	for _, id := range ids {
		var state string
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_threshold_events WHERE account_id=$1`, id).Scan(&state))
		require.Equal(t, "suppressed", state)
	}
}
