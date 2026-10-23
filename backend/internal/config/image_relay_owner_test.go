package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageRelayOwnerOrigin(t *testing.T) {
	for _, origin := range []string{"", "https://node-a.example.invalid", "https://node-a.example.invalid/", "https://node-a.example.invalid:8443"} {
		require.NoError(t, ValidateImageRelayOwnerOrigin(origin))
	}
	for _, origin := range []string{"http://node-a.example.invalid", "//node-a.example.invalid", "https://", "https://user:password@node-a.example.invalid", "https://node-a.example.invalid/path", "https://node-a.example.invalid/%2f", "https://node-a.example.invalid?", "https://node-a.example.invalid?token=fixture", "https://node-a.example.invalid#", "https://node-a.example.invalid/#fragment"} {
		err := ValidateImageRelayOwnerOrigin(origin)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "password")
		require.NotContains(t, err.Error(), "token=fixture")
	}
	cfg := &Config{Runtime: RuntimeConfig{ImageRelayOwnerOrigin: "http://node-a.example.invalid"}}
	require.Error(t, cfg.validateRuntime())
}

func TestImageRelayOwnerOriginEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("RUNTIME_IMAGE_RELAY_OWNER_ORIGIN", "https://node-a.example.invalid")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "https://node-a.example.invalid", cfg.Runtime.ImageRelayOwnerOrigin)
}
