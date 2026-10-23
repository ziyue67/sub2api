package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeNodeIdentity(t *testing.T) {
	for _, id := range []string{"", "node-a_1"} {
		cfg := &Config{Runtime: RuntimeConfig{NodeID: id}}
		require.NoError(t, cfg.validateRuntime())
	}
	for _, id := range []string{"node.example", "private:secret", "node\nlog", "NodeA"} {
		cfg := &Config{Runtime: RuntimeConfig{NodeID: id}}
		err := cfg.validateRuntime()
		require.Error(t, err)
		require.NotContains(t, err.Error(), id)
	}
	cfg := &Config{Runtime: RuntimeConfig{NodeID: "node-a", WSNodeOwner: WSNodeOwnerConfig{Enabled: true, NodeID: "node-b"}}}
	require.Error(t, cfg.validateRuntime())
	cfg.Runtime.WSNodeOwner.NodeID = "node-a"
	require.NoError(t, cfg.validateRuntime())
}

func TestRuntimeNodeIdentityEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RUNTIME_NODE_ID", "node-a")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "node-a", cfg.Runtime.NodeID)
}
