package authguard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistrationOriginNormalization(t *testing.T) {
	key := IPKey("192.0.2.1")
	require.Len(t, key, 64)
	require.Equal(t, key, IPKey("::ffff:192.0.2.1"))
	require.Equal(t, IPKey("2001:db8::1"), IPKey("2001:0db8:0:0::1"))
	require.Equal(t, IPKey("2001:db8::1"), IPKey("2001:db8::ffff:ffff:ffff:ffff"), "IPv6 host rotation shares /64 quota and bans")
	require.NotEqual(t, IPKey("2001:db8::1"), IPKey("2001:db8:0:1::1"), "different /64 networks remain independent")
	require.NotEqual(t, key, IPKey("192.0.2.2"))
	for _, address := range []string{"", "unknown", "192.0.2.1:1234", "fe80::1%eth0"} {
		require.Empty(t, IPKey(address))
	}
	_, enabled := RegistrationIP(context.Background())
	require.False(t, enabled, "admin/internal creation is not public signup")
	stored, enabled := RegistrationIP(WithRegistrationIP(context.Background(), "unknown"))
	require.True(t, enabled, "invalid origin cannot disable quota enforcement")
	require.Empty(t, stored)
	stored, enabled = RegistrationIP(WithRegistrationIP(context.Background(), "192.0.2.1"))
	require.True(t, enabled)
	require.Equal(t, key, stored)
}
