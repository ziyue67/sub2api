//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func groupCriteriaFixture(t *testing.T, kind string) (service.AccountOpsConfig, service.AccountOpsEvent) {
	t.Helper()
	ids := make([]int64, 2)
	for i := range ids {
		require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('group-criteria','openai','apikey','active',true) RETURNING id`).Scan(&ids[i]))
		id := ids[i]
		t.Cleanup(func() { cleanupOnce(t, id) })
	}
	groupID := fmt.Sprintf("criteria-group:%d", ids[0])
	cfg := service.AccountOpsConfig{Enabled: true, GroupsInitialized: true, Groups: []service.AccountOpsGroup{{ID: groupID, AccountIDs: ids}}}
	for _, id := range ids {
		if kind == "balance_threshold" {
			cfg.BalanceThresholds = append(cfg.BalanceThresholds, service.AccountOpsBalanceRule{AccountID: id, Enabled: true, Threshold: 10, Unit: "USD"})
		} else {
			cfg.QuotaThresholds = append(cfg.QuotaThresholds, service.AccountOpsQuotaRule{AccountID: id, Enabled: true, ThresholdPercent: 20, Window: "any"})
		}
	}
	criteria, enabled, _, _ := cfg.ThresholdGroupCriteria(groupID, kind)
	require.True(t, enabled)
	return cfg, service.AccountOpsEvent{AccountID: ids[0], GroupID: groupID, Kind: kind, Identity: groupID, Criteria: criteria}
}

func TestAccountOpsGroupedCriteriaSurvivesRetryAndMemberOrdering(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"balance_threshold", "quota_threshold"} {
		t.Run(kind, func(t *testing.T) {
			cfg, e := groupCriteriaFixture(t, kind)
			repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
			require.NoError(t, repo.ObserveThreshold(ctx, e, "active", time.Now(), true, true))
			first, err := repo.Claim(ctx)
			require.NoError(t, err)
			require.NotNil(t, first)
			require.NoError(t, repo.Complete(ctx, first, "failed", 5*time.Minute))

			// Ordering does not change the composite fingerprint or reset the episode.
			cfg.Groups[0].AccountIDs[0], cfg.Groups[0].AccountIDs[1] = cfg.Groups[0].AccountIDs[1], cfg.Groups[0].AccountIDs[0]
			if kind == "balance_threshold" {
				cfg.BalanceThresholds[0], cfg.BalanceThresholds[1] = cfg.BalanceThresholds[1], cfg.BalanceThresholds[0]
			} else {
				cfg.QuotaThresholds[0], cfg.QuotaThresholds[1] = cfg.QuotaThresholds[1], cfg.QuotaThresholds[0]
			}
			restarted := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
			require.NoError(t, restarted.SuppressDisabled(ctx, cfg))
			var state string
			var delayKept bool
			require.NoError(t, integrationDB.QueryRow(`SELECT state,next_send_at>NOW()+INTERVAL '4 minutes' FROM account_ops_threshold_events WHERE id=$1`, first.ID).Scan(&state, &delayKept))
			require.Equal(t, "failed", state, "an unchanged group must retain its retry")
			require.True(t, delayKept)
			waiting, err := restarted.Claim(ctx)
			require.NoError(t, err)
			require.Nil(t, waiting)

			// Advance the persisted retry deadline instead of sleeping five minutes.
			_, err = integrationDB.Exec(`UPDATE account_ops_threshold_events SET next_send_at=NOW()-INTERVAL '1 second' WHERE id=$1`, first.ID)
			require.NoError(t, err)
			retry, err := restarted.Claim(ctx)
			require.NoError(t, err)
			require.NotNil(t, retry)
			require.Equal(t, first.ID, retry.ID)
			require.Equal(t, 2, retry.Attempts)
			require.NoError(t, restarted.Complete(ctx, retry, "sent", time.Hour))
		})
	}
}

func TestAccountOpsGroupedCriteriaSuppressesRealConfigChanges(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		recovery bool
		mutate   func(*service.AccountOpsConfig)
	}{
		{"master disabled", false, func(c *service.AccountOpsConfig) { c.Enabled = false }},
		{"all rules disabled", false, func(c *service.AccountOpsConfig) {
			for i := range c.BalanceThresholds {
				c.BalanceThresholds[i].Enabled = false
			}
		}},
		{"member disabled", false, func(c *service.AccountOpsConfig) { c.BalanceThresholds[1].Enabled = false }},
		{"rule deleted", false, func(c *service.AccountOpsConfig) { c.BalanceThresholds = c.BalanceThresholds[:1] }},
		{"member removed", false, func(c *service.AccountOpsConfig) { c.Groups[0].AccountIDs = c.Groups[0].AccountIDs[:1] }},
		{"group deleted", false, func(c *service.AccountOpsConfig) { c.Groups = nil }},
		{"threshold changed", false, func(c *service.AccountOpsConfig) { c.BalanceThresholds[1].Threshold++ }},
		{"unit changed", false, func(c *service.AccountOpsConfig) { c.BalanceThresholds[1].Unit = "CNY" }},
		{"alert disabled", false, func(c *service.AccountOpsConfig) {
			flag := false
			for i := range c.BalanceThresholds {
				c.BalanceThresholds[i].NotifyAlert = &flag
			}
		}},
		{"recovery disabled", true, func(c *service.AccountOpsConfig) { flag := false; c.BalanceThresholds[1].NotifyRecovery = &flag }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, e := groupCriteriaFixture(t, "balance_threshold")
			repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
			now := time.Now()
			require.NoError(t, repo.ObserveThreshold(ctx, e, "active", now, true, true))
			if tc.recovery {
				require.NoError(t, repo.ObserveThreshold(ctx, e, "resolved", now.Add(time.Second), true, true))
				require.NoError(t, repo.ObserveThreshold(ctx, e, "resolved", now.Add(16*time.Second), true, true))
			}
			claimed, err := repo.Claim(ctx)
			require.NoError(t, err)
			require.NotNil(t, claimed)
			tc.mutate(&cfg)
			require.NoError(t, repo.SuppressDisabled(ctx, cfg))
			var state string
			require.NoError(t, integrationDB.QueryRow(`SELECT state FROM account_ops_threshold_events WHERE id=$1`, claimed.ID).Scan(&state))
			require.Equal(t, "suppressed", state)
			valid, err := repo.DeliveryLeaseValid(ctx, claimed)
			require.NoError(t, err)
			require.False(t, valid, "configuration changes also invalidate a sending lease")
		})
	}
}

func TestAccountOpsGroupedCriteriaConfigSaveKeepsPendingEvent(t *testing.T) {
	ctx := context.Background()
	cfg, e := groupCriteriaFixture(t, "balance_threshold")
	repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
	require.NoError(t, repo.ObserveThreshold(ctx, e, "active", time.Now(), true, true))
	// Use a rollback-only transaction so the shared settings fixture is unchanged.
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, repo.PersistAccountOpsConfig(context.WithValue(ctx, accountOpsTransactionKey{}, tx), string(raw), cfg))
	var state string
	require.NoError(t, tx.QueryRow(`SELECT state FROM account_ops_threshold_events WHERE group_id=$1`, e.GroupID).Scan(&state))
	require.Equal(t, "pending", state)
}

func TestAccountOpsGroupedCriteriaIdleCheckUsesWholeGroup(t *testing.T) {
	ctx := context.Background()
	cfg, e := groupCriteriaFixture(t, "balance_threshold")
	cfg.BalanceThresholds[0].Enabled = false
	e.Criteria, _, _, _ = cfg.ThresholdGroupCriteria(e.GroupID, e.Kind)
	repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
	require.NoError(t, repo.ObserveThreshold(ctx, e, "resolved", time.Now(), true, true))
	readOnlyDB, err := sql.Open("postgres", integrationPostgresDSN)
	require.NoError(t, err)
	defer readOnlyDB.Close()
	readOnlyDB.SetMaxOpenConns(1)
	_, err = readOnlyDB.ExecContext(ctx, `SET default_transaction_read_only=on`)
	require.NoError(t, err)
	require.NoError(t, NewAccountOpsRepository(readOnlyDB).SuppressDisabled(ctx, cfg), "a disabled representative does not invalidate an enabled group")

	cfg.Groups = nil
	require.NoError(t, repo.SuppressDisabled(ctx, cfg))
	var criteria string
	require.NoError(t, integrationDB.QueryRow(`SELECT criteria FROM account_ops_threshold_monitors WHERE group_id=$1`, e.GroupID).Scan(&criteria))
	require.Equal(t, "invalid", criteria, "a deleted group is invalidated even with no pending event")
}
