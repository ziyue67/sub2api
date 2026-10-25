package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountOpsGroupCriteriaMatchesScannerAndIgnoresOrdering(t *testing.T) {
	for _, kind := range []string{"balance_threshold", "quota_threshold"} {
		t.Run(kind, func(t *testing.T) {
			cfg := defaultAccountOpsConfig()
			cfg.Groups = []AccountOpsGroup{{ID: "manual-example1", AccountIDs: []int64{2, 1, 3}}}
			cfg.BalanceThresholds = []AccountOpsBalanceRule{
				{AccountID: 2, Enabled: true, Threshold: 5, Unit: "USD", NotifyAlert: opsBool(false), NotifyRecovery: opsBool(true)},
				{AccountID: 1, Enabled: true, Threshold: 7, Unit: "USD", NotifyAlert: opsBool(true), NotifyRecovery: opsBool(false)},
				{AccountID: 3, Enabled: false, Threshold: 9, Unit: "USD"},
				{AccountID: 4, Enabled: true, Threshold: 1, Unit: "USD"},
			}
			cfg.QuotaThresholds = []AccountOpsQuotaRule{
				{AccountID: 2, Enabled: true, ThresholdPercent: 80, Window: "5h", NotifyAlert: opsBool(false), NotifyRecovery: opsBool(true)},
				{AccountID: 1, Enabled: true, ThresholdPercent: 90, Window: "7d", NotifyAlert: opsBool(true), NotifyRecovery: opsBool(false)},
				{AccountID: 3, Enabled: false, ThresholdPercent: 70, Window: "any"},
				{AccountID: 4, Enabled: true, ThresholdPercent: 50, Window: "any"},
			}
			want, enabled, alert, recovery := accountOpsGroupCriteria(cfg, kind, []accountOpsThresholdMember{{id: 2}, {id: 1}, {id: 3}})
			got, gotEnabled, gotAlert, gotRecovery := cfg.ThresholdGroupCriteria("manual-example1", kind)
			require.Equal(t, want, got)
			require.Equal(t, []bool{enabled, alert, recovery}, []bool{gotEnabled, gotAlert, gotRecovery})
			require.True(t, gotEnabled)
			require.True(t, gotAlert)
			require.False(t, gotRecovery)

			cfg.Groups[0].AccountIDs = []int64{3, 1, 2}
			cfg.BalanceThresholds[0], cfg.BalanceThresholds[1] = cfg.BalanceThresholds[1], cfg.BalanceThresholds[0]
			cfg.QuotaThresholds[0], cfg.QuotaThresholds[1] = cfg.QuotaThresholds[1], cfg.QuotaThresholds[0]
			reordered, _, _, _ := cfg.ThresholdGroupCriteria("manual-example1", kind)
			require.Equal(t, got, reordered)

			fallback, enabled, _, _ := cfg.ThresholdGroupCriteria("account:4", kind)
			individual, _, _, _ := cfg.ThresholdCriteria(4, kind)
			require.True(t, enabled)
			require.Equal(t, individual, fallback)
			missing, enabled, _, _ := cfg.ThresholdGroupCriteria("missing", kind)
			require.Empty(t, missing)
			require.False(t, enabled)
		})
	}
}
