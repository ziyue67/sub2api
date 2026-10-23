package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchedulerSnapshotModeDefaultsAndValidation(t *testing.T) {
	require.True(t, (*Config)(nil).WritesSchedulerSnapshots())
	for _, tc := range []struct {
		role, mode    string
		writer, valid bool
	}{
		{"", "", true, true}, {RuntimeRoleFull, SchedulerSnapshotModeAuto, true, true},
		{RuntimeRoleGateway, "", false, true}, {RuntimeRoleGateway, SchedulerSnapshotModeAuto, false, true},
		{RuntimeRoleFull, SchedulerSnapshotModeWriter, true, true},
		{RuntimeRoleFull, SchedulerSnapshotModeReader, false, true},
		{RuntimeRoleGateway, SchedulerSnapshotModeReader, false, true},
		{RuntimeRoleGateway, SchedulerSnapshotModeWriter, false, false},
		{RuntimeRoleFull, "raeder", false, false},
	} {
		t.Run(tc.role+"/"+tc.mode, func(t *testing.T) {
			cfg := &Config{Runtime: RuntimeConfig{Role: tc.role, SchedulerSnapshotMode: tc.mode}}
			require.Equal(t, tc.writer, cfg.WritesSchedulerSnapshots())
			if tc.valid {
				require.NoError(t, cfg.validateRuntime())
			} else {
				require.ErrorContains(t, cfg.validateRuntime(), "runtime.scheduler_snapshot_mode")
			}
		})
	}
}

func TestSchedulerSnapshotModeEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RUNTIME_SCHEDULER_SNAPSHOT_MODE", "reader")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, SchedulerSnapshotModeReader, cfg.Runtime.SchedulerSnapshotMode)
	require.False(t, cfg.WritesSchedulerSnapshots())
}
