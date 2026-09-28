package service

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExcelBPS403RecoveryCustomInterval(t *testing.T) {
	now := time.Now().UTC()
	for _, minutes := range []int{1, 30, 60, 360, 10080} {
		t.Run(fmt.Sprint(minutes), func(t *testing.T) {
			a := recoveryTestAccount(now)
			a.Extra[ExcelBPS403RecoveryIntervalMinutesKey] = minutes
			a.Extra[ExcelBPS403DisabledAtKey] = now.Format(time.RFC3339Nano)
			interval := time.Duration(minutes) * time.Minute
			require.False(t, a.ExcelBPS403RecoveryDue(now.Add(interval-time.Nanosecond)))
			require.True(t, a.ExcelBPS403RecoveryDue(now.Add(interval)))
			attempted := now.Add(interval)
			a.Extra[ExcelBPS403LastProbeAtKey] = attempted.Format(time.RFC3339Nano)
			require.False(t, a.ExcelBPS403RecoveryDue(attempted.Add(interval-time.Nanosecond)))
			require.True(t, a.ExcelBPS403RecoveryDue(attempted.Add(interval)))
		})
	}
	a := recoveryTestAccount(now)
	a.Extra[ExcelBPS403LastProbeAtKey] = now.Add(-30 * time.Minute).Format(time.RFC3339Nano)
	a.Extra[ExcelBPS403RecoveryIntervalMinutesKey] = 30
	require.True(t, a.ExcelBPS403RecoveryDue(now))
	a.Extra[ExcelBPS403RecoveryIntervalMinutesKey] = 360
	require.False(t, a.ExcelBPS403RecoveryDue(now))
	require.True(t, a.ExcelBPS403RecoveryDue(now.Add(330*time.Minute)))
}

func TestExcelBPS403RecoveryIntervalValidation(t *testing.T) {
	for _, raw := range []any{nil, 0, -1, 1.5, 10081, "30", true, math.NaN(), math.Inf(1), json.Number("1.5")} {
		t.Run(fmt.Sprintf("invalid_%v", raw), func(t *testing.T) {
			extra := map[string]any{ExcelBPS403RecoveryIntervalMinutesKey: raw}
			require.Error(t, validateExcelBPS403RecoveryExtra(extra))
			changed, err := normalizeBulkExcelBPSExtra(extra)
			require.True(t, changed)
			require.Error(t, err)
			a := recoveryTestAccount(time.Now())
			a.Extra = extra
			require.Error(t, (&adminServiceImpl{}).validateExcelBPS403GroupSettings(t.Context(), a))
			require.Equal(t, time.Hour, a.ExcelBPS403RecoveryInterval())
		})
	}
	for _, raw := range []any{1, 30, 360, 10080, float64(360), int64(30), json.Number("360")} {
		extra := map[string]any{ExcelBPS403RecoveryIntervalMinutesKey: raw}
		require.NoError(t, validateExcelBPS403RecoveryExtra(extra))
		changed, err := normalizeBulkExcelBPSExtra(extra)
		require.NoError(t, err)
		require.True(t, changed)
		a := recoveryTestAccount(time.Now())
		a.Extra = extra
		require.NoError(t, (&adminServiceImpl{}).validateExcelBPS403GroupSettings(t.Context(), a))
	}
	require.NoError(t, validateExcelBPS403RecoveryExtra(nil))
	require.Equal(t, time.Hour, (&Account{}).ExcelBPS403RecoveryInterval())
	require.Equal(t, time.Hour, (*Account)(nil).ExcelBPS403RecoveryInterval())
}

func TestQualityBPSRecoveryInterval(t *testing.T) {
	policy := &QualityBPSPolicy{FailureThreshold: 2, AllModels: true, AutoDisableOn403: true, AutoRecoverOn403: true}
	require.NoError(t, validateQualityBPSPolicy(policy))
	require.Equal(t, 60, QualityBPSExtra(policy)[ExcelBPS403RecoveryIntervalMinutesKey])
	require.Contains(t, QualityBPSManagedKeys, ExcelBPS403RecoveryIntervalMinutesKey)
	for _, minutes := range []int{1, 30, 360, 10080, 0, -1, 10081} {
		policy.RecoveryIntervalMinutes = &minutes
		err := validateQualityBPSPolicy(policy)
		if minutes < 1 || minutes > 10080 {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, minutes, QualityBPSExtra(policy)[ExcelBPS403RecoveryIntervalMinutesKey])
		}
	}
	for _, raw := range []string{"1.5", "\"30\"", "true"} {
		require.Error(t, json.Unmarshal([]byte("{\"recovery_interval_minutes\":"+raw+"}"), &policy))
	}
}
