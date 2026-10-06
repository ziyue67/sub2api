package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAstraSchedulingModesValidate(t *testing.T) {
	s := AstraRoutingSettings{}
	require.Equal(t, "account", s.EffectiveSchedulingMode())
	require.NoError(t, s.Validate())
	s.AccountScheduling = true
	s.SchedulingMode = "groups"
	require.Error(t, s.Validate())
	s.SchedulingGroupIDs = []int64{1, 2}
	require.NoError(t, s.Validate())
	s.SchedulingGroupIDs = []int64{1, 1}
	require.Error(t, s.Validate())
	s.SchedulingMode = "invalid"
	require.Error(t, s.Validate())
}
