package config

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/upstreamroute"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUpstreamRouting(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("TEST_REGIONAL_PROXY", "socks5h://127.0.0.1:1081")
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`gateway:
  upstream_routing:
    enabled: true
    regions:
      - id: us
        proxy_url_env: TEST_REGIONAL_PROXY
    rules:
      - domain: api.example.com
        region: us
        account_ids: [101]
`), 0600))
	t.Setenv("CONFIG_FILE", path)
	c, err := Load()
	require.NoError(t, err)
	require.True(t, c.Gateway.UpstreamRouting.Enabled)
	require.Equal(t, []upstreamroute.Rule{{Domain: "api.example.com", Region: "us", AccountIDs: []int64{101}}}, c.Gateway.UpstreamRouting.Rules)
	t.Setenv("TEST_REGIONAL_PROXY", "")
	_, err = Load()
	require.ErrorContains(t, err, "gateway.upstream_routing")
	t.Setenv("GATEWAY_UPSTREAM_ROUTING_ENABLED", "false")
	c, err = Load()
	require.NoError(t, err)
	require.False(t, c.Gateway.UpstreamRouting.Enabled)
}
func TestUpstreamRoutingDisabledByDefault(t *testing.T) {
	resetViperWithJWTSecret(t)
	c, err := Load()
	require.NoError(t, err)
	require.False(t, c.Gateway.UpstreamRouting.Enabled)
}
