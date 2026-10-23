package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestIPRiskTrustedProxiesIncludesEffectiveDefaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{name: "missing config"},
		{name: "default loopback proxies", cfg: &config.Config{}, want: true},
		{name: "explicit empty disables trust", cfg: &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: true}}},
		{name: "explicit remote proxy", cfg: &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{"192.0.2.1/32"}}}, want: true},
		{name: "invalid proxy disables trust", cfg: &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{"127.0.0.1", "invalid"}}}},
		{name: "whitespace rejected just like Gin", cfg: &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{" 127.0.0.1 "}}}},
		{name: "mapped CIDR accepted just like Gin", cfg: &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{"::ffff:192.0.2.1/80"}}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &SettingService{cfg: tc.cfg}
			require.Equal(t, tc.want, s.IPRiskTrustedProxiesConfigured())
		})
	}
}
