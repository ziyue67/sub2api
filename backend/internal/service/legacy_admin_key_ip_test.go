//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type legacyIPPolicyRepo struct {
	SettingRepository
	value  string
	err    error
	writes int
}

func (r *legacyIPPolicyRepo) GetValue(context.Context, string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.value == "" {
		return "", ErrSettingNotFound
	}
	return r.value, nil
}
func (r *legacyIPPolicyRepo) Set(_ context.Context, _ string, value string) error {
	if r.err != nil {
		return r.err
	}
	r.value = value
	r.writes++
	return nil
}

func TestLegacyAdminKeyIPNormalize(t *testing.T) {
	in := LegacyAdminKeyIPPolicy{true, []string{" 192.0.2.1 ", "::ffff:192.0.2.1", "2001:0db8::1/64", "::ffff:192.0.2.9/120"}}
	p, e := NormalizeLegacyAdminKeyIPPolicy(in)
	require.NoError(t, e)
	require.Equal(t, []string{"192.0.2.1", "2001:db8::/64", "192.0.2.0/24"}, p.AllowedIPs)
	for _, ip := range []string{"192.0.2.1", "::ffff:192.0.2.2", "2001:db8::123"} {
		require.True(t, p.Allows(ip), ip)
	}
	for _, ip := range []string{"192.0.3.1", "invalid", "", "fe80::1%eth0"} {
		require.False(t, p.Allows(ip), ip)
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "::ffff:0.0.0.0/96", "::ffff:0:0/80", "example.com", "https://192.0.2.1", "192.0.2.1:80", "[::1]:80", "*", "192.0.2.1/33", "fe80::1%eth0", ""} {
		_, e = NormalizeLegacyAdminKeyIPPolicy(LegacyAdminKeyIPPolicy{false, []string{bad}})
		require.Error(t, e, bad)
	}
	_, e = NormalizeLegacyAdminKeyIPPolicy(LegacyAdminKeyIPPolicy{Enabled: true})
	require.Error(t, e)
	_, e = NormalizeLegacyAdminKeyIPPolicy(LegacyAdminKeyIPPolicy{AllowedIPs: strings.Split(strings.Repeat("1.2.3.4,", 100)+"1.2.3.4", ",")})
	require.Error(t, e)
}

func TestLegacyAdminKeyIPAtomicLiveReads(t *testing.T) {
	ctx := context.Background()
	repo := &legacyIPPolicyRepo{}
	a := NewSettingService(repo, nil)
	b := NewSettingService(repo, nil)
	p, e := a.GetLegacyAdminKeyIPPolicy(ctx)
	require.NoError(t, e)
	require.False(t, p.Enabled)
	_, e = a.SetLegacyAdminKeyIPPolicy(ctx, LegacyAdminKeyIPPolicy{true, []string{"192.0.2.1"}})
	require.NoError(t, e)
	require.Equal(t, 1, repo.writes)
	p, e = b.GetLegacyAdminKeyIPPolicy(ctx)
	require.NoError(t, e)
	require.True(t, p.Allows("192.0.2.1"))
	require.False(t, p.Allows("192.0.2.2"))
	previous := repo.value
	_, e = a.SetLegacyAdminKeyIPPolicy(ctx, LegacyAdminKeyIPPolicy{Enabled: true})
	require.Error(t, e)
	require.Equal(t, previous, repo.value)
	_, e = a.SetLegacyAdminKeyIPPolicy(ctx, LegacyAdminKeyIPPolicy{false, []string{"192.0.2.1"}})
	require.NoError(t, e)
	p, e = b.GetLegacyAdminKeyIPPolicy(ctx)
	require.NoError(t, e)
	require.True(t, p.Allows("192.0.2.2"))
	for _, raw := range []string{`null`, `{}`, `{"enabled":false}`, `{"enabled":true,"allowed_ips":[]}`, `{"enabled":false,"allowed_ips":null}`, `{"enabled":false,"allowed_ips":["bad"]}`, `broken`} {
		repo.value = raw
		_, e = b.GetLegacyAdminKeyIPPolicy(ctx)
		require.Error(t, e, raw)
	}
	repo.err = errors.New("database down")
	_, e = b.GetLegacyAdminKeyIPPolicy(ctx)
	require.Error(t, e)
}
