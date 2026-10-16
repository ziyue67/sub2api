// Package authguard carries the trusted registration origin to the persistence boundary.
package authguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strings"
)

type registrationIPKey struct{}

// IPKey canonicalizes IPv4 (including mapped addresses) and aggregates native
// IPv6 to /64 before hashing, preventing quota evasion by rotating host bits.
// Invalid addresses have no key and must not bypass an enabled guard.
func IPKey(address string) string {
	ip, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || ip.Zone() != "" {
		return ""
	}
	ip = ip.Unmap()
	origin := ip.String()
	if ip.Is6() {
		origin = netip.PrefixFrom(ip, 64).Masked().String()
	}
	sum := sha256.Sum256([]byte(origin))
	return hex.EncodeToString(sum[:])
}

// WithRegistrationIP enables lifetime registration accounting for this request.
// Keep the marker even for an invalid IP so persistence can fail closed.
func WithRegistrationIP(ctx context.Context, address string) context.Context {
	return context.WithValue(ctx, registrationIPKey{}, IPKey(address))
}

// RegistrationIP distinguishes public signup requests from admin/CLI creation.
func RegistrationIP(ctx context.Context) (string, bool) {
	key, enabled := ctx.Value(registrationIPKey{}).(string)
	return key, enabled
}
