package config

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRuntimeRolesPreserveDefaultsAndRejectTypos(t *testing.T) {
	require.True(t, (*Config)(nil).RunsBackgroundJobs())
	for _, role := range []string{"", RuntimeRoleFull, RuntimeRoleGateway} {
		cfg := &Config{Runtime: RuntimeConfig{Role: role}}
		require.NoError(t, cfg.validateRuntime())
		require.Equal(t, role != RuntimeRoleGateway, cfg.RunsBackgroundJobs())
	}
	cfg := &Config{Runtime: RuntimeConfig{Role: "gateawy"}}
	require.ErrorContains(t, cfg.validateRuntime(), "runtime.role")
}

func TestRuntimeDrainBudgets(t *testing.T) {
	require.Equal(t, 5*time.Second, (ServerConfig{}).ShutdownTimeout())
	cfg := &Config{Server: ServerConfig{GracefulShutdownTimeout: 240, ShutdownDrainDelay: 10}}
	require.NoError(t, cfg.validateRuntime())
	require.Equal(t, 240*time.Second, cfg.Server.ShutdownTimeout())
	for _, value := range []int{-1, 3601} {
		cfg.Server.GracefulShutdownTimeout = value
		require.Error(t, cfg.validateRuntime())
	}
	cfg.Server.GracefulShutdownTimeout = 240
	for _, value := range []int{-1, 301} {
		cfg.Server.ShutdownDrainDelay = value
		require.Error(t, cfg.validateRuntime())
	}
}

func TestRuntimeRoleAndDrainEnvironmentOverrides(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RUNTIME_ROLE", "gateway")
	t.Setenv("SERVER_GRACEFUL_SHUTDOWN_TIMEOUT", "240")
	t.Setenv("SERVER_SHUTDOWN_DRAIN_DELAY", "10")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, RuntimeRoleGateway, cfg.Runtime.Role)
	require.False(t, cfg.RunsBackgroundJobs())
	require.Equal(t, 240*time.Second, cfg.Server.ShutdownTimeout())
	require.Equal(t, 10, cfg.Server.ShutdownDrainDelay)
}
