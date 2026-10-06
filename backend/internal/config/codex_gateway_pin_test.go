package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCodexGatewayPinConfigValidation(t *testing.T) {
	require.NoError(t, (CodexGatewayPinConfig{}).Validate())
	for _, c := range []CodexGatewayPinConfig{
		{Enabled: true},
		{Enabled: true, SourceAccountIDs: []int64{299}},
		{Enabled: true, SourceAccountIDs: []int64{299}, TargetAccountIDs: []int64{299}},
		{Enabled: true, SourceAccountIDs: []int64{-1}, TargetAccountIDs: []int64{300}},
	} {
		require.Error(t, c.Validate())
	}
	require.NoError(t, (CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{299}, TargetAccountIDs: []int64{300}}).Validate())
}

func TestCodexWSAnchorConfigValidation(t *testing.T) {
	require.NoError(t, (CodexWSAnchorConfig{}).Validate())
	require.Error(t, (CodexWSAnchorConfig{Enabled: true}).Validate())
	require.Error(t, (CodexWSAnchorConfig{Enabled: true, AccountIDs: []int64{300, 300}}).Validate())
	require.NoError(t, (CodexWSAnchorConfig{Enabled: true, AccountIDs: []int64{299, 300}}).Validate())
}
