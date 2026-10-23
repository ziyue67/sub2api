package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWSNodeOwnerConfiguration(t *testing.T) {
	for _, node := range []string{"", "NodeA", "node.example", "node:a", "node a"} {
		require.Error(t, (WSNodeOwnerConfig{Enabled: true, NodeID: node}).Validate())
	}
	require.NoError(t, (WSNodeOwnerConfig{}).Validate())
	require.NoError(t, (WSNodeOwnerConfig{Enabled: true, NodeID: "node-a_1"}).Validate())
	cfg := &Config{Runtime: RuntimeConfig{WSNodeOwner: WSNodeOwnerConfig{Enabled: true}}}
	require.Error(t, cfg.validateRuntime(), "runtime validation must not bypass owner identity validation")
}

func TestWSNodeOwnerEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RUNTIME_WS_NODE_OWNER_ENABLED", "true")
	t.Setenv("RUNTIME_WS_NODE_OWNER_NODE_ID", "node-a")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Runtime.WSNodeOwner.Enabled)
	require.Equal(t, "node-a", cfg.Runtime.WSNodeOwner.NodeID)
}
