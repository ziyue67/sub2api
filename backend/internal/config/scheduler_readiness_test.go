package config

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSchedulerReadinessConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		role     string
		enabled  bool
		buckets  []string
		age      int
		regional string
		invalid  bool
	}{
		{name: "disabled legacy"},
		{name: "reader", role: RuntimeRoleGateway, enabled: true, buckets: []string{"17:openai:single"}},
		{name: "full rejected", role: RuntimeRoleFull, enabled: true, buckets: []string{"17:openai:single"}, invalid: true},
		{name: "empty scopes", role: RuntimeRoleGateway, enabled: true, invalid: true},
		{name: "invalid group", role: RuntimeRoleGateway, enabled: true, buckets: []string{"-1:openai:single"}, invalid: true},
		{name: "invalid mode", role: RuntimeRoleGateway, enabled: true, buckets: []string{"17:openai:mixed"}, invalid: true},
		{name: "duplicate", role: RuntimeRoleGateway, enabled: true, buckets: []string{"17:openai:single", "17:openai:single"}, invalid: true},
		{name: "regional preview rejected", role: RuntimeRoleGateway, enabled: true, buckets: []string{"17:openai:single"}, regional: "preview.json", invalid: true},
		{name: "negative age", age: -1, invalid: true},
		{name: "unbounded age", age: 86401, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Runtime: RuntimeConfig{Role: tc.role, RegionalSchedulerFile: tc.regional, SchedulerReadiness: SchedulerReadinessConfig{Enabled: tc.enabled, Buckets: tc.buckets, MaxAgeSeconds: tc.age}}}
			err := c.validateRuntime()
			if tc.invalid {
				require.ErrorContains(t, err, "runtime.scheduler_readiness")
			} else {
				require.NoError(t, err)
			}
		})
	}
	require.Equal(t, 15*time.Minute, (SchedulerReadinessConfig{}).MaxAge())
}

func TestSchedulerReadinessEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RUNTIME_ROLE", "gateway")
	t.Setenv("RUNTIME_SCHEDULER_READINESS_ENABLED", "true")
	t.Setenv("RUNTIME_SCHEDULER_READINESS_BUCKETS", "17:openai:single")
	t.Setenv("RUNTIME_SCHEDULER_READINESS_MAX_AGE_SECONDS", "60")
	c, err := Load()
	require.NoError(t, err)
	require.True(t, c.Runtime.SchedulerReadiness.Enabled)
	require.Equal(t, []string{"17:openai:single"}, c.Runtime.SchedulerReadiness.Buckets)
	require.Equal(t, time.Minute, c.Runtime.SchedulerReadiness.MaxAge())
}
