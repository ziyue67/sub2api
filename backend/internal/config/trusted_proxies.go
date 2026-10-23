package config

// EffectiveTrustedProxies defaults to proxies on this host only. Local reverse
// proxies must overwrite X-Real-IP or append the actual peer to X-Forwarded-For.
// Explicit configuration, including an empty list, replaces these defaults.
// Docker bridges and other private networks always require explicit trust.
func (c ServerConfig) EffectiveTrustedProxies() []string {
	if c.TrustedProxiesConfigured || len(c.TrustedProxies) > 0 {
		return c.TrustedProxies
	}
	return []string{"127.0.0.1/32", "::1/128"}
}
