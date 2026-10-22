package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type loginRateLimitSettingRepo struct {
	SettingRepository
	value string
	err   error
}

func (r *loginRateLimitSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, r.err
}

func TestLoginRateLimitDefaultsAndLiveChanges(t *testing.T) {
	repo := &loginRateLimitSettingRepo{err: ErrSettingNotFound}
	s := NewSettingService(repo, &config.Config{})
	ctx := context.Background()
	require.True(t, s.IsLoginRateLimitEnabled(ctx), "upgrades keep existing protection")
	repo.err = nil
	for _, value := range []string{"", "true", "invalid", "0"} {
		repo.value = value
		require.True(t, s.IsLoginRateLimitEnabled(ctx))
	}
	repo.value = "false"
	require.False(t, s.IsLoginRateLimitEnabled(ctx))
	repo.value = "true"
	require.True(t, s.IsLoginRateLimitEnabled(ctx), "no restart or cache expiry required")
	repo.value = "false"
	repo.err = errors.New("database unavailable")
	require.True(t, s.IsLoginRateLimitEnabled(ctx), "lookup errors must not disable protection")
	var absent *SettingService
	require.True(t, absent.IsLoginRateLimitEnabled(ctx))
}
