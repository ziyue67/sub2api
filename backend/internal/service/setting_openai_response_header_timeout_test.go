package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newOpenAIHeaderTimeoutTestService(t *testing.T, seconds int) *SettingService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.OpenAIResponseHeaderTimeout = seconds
	return NewSettingService(&panelRateLimitSettingRepo{values: map[string]string{}}, cfg)
}

func TestDefaultOpenAIResponseHeaderTimeoutSettings(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIResponseHeaderTimeout = 20
	settings := DefaultOpenAIResponseHeaderTimeoutSettings(cfg)
	require.True(t, settings.Enabled)
	require.Equal(t, 20, settings.Seconds)

	cfg.Gateway.OpenAIResponseHeaderTimeout = 0
	settings = DefaultOpenAIResponseHeaderTimeoutSettings(cfg)
	require.False(t, settings.Enabled)
	require.Equal(t, openAIResponseHeaderTimeoutDefaultSeconds, settings.Seconds)

	settings = DefaultOpenAIResponseHeaderTimeoutSettings(nil)
	require.True(t, settings.Enabled)
	require.Equal(t, openAIResponseHeaderTimeoutDefaultSeconds, settings.Seconds)
}

func TestOpenAIResponseHeaderTimeoutSettingsGetMissingUsesConfig(t *testing.T) {
	svc := newOpenAIHeaderTimeoutTestService(t, 45)

	settings, err := svc.GetOpenAIResponseHeaderTimeoutSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 45, settings.Seconds)

	seconds, enabled := svc.GetOpenAIResponseHeaderTimeoutCached(context.Background())
	require.True(t, enabled)
	require.Equal(t, 45, seconds)
}

func TestOpenAIResponseHeaderTimeoutSettingsSetAndCache(t *testing.T) {
	svc := newOpenAIHeaderTimeoutTestService(t, 20)

	require.NoError(t, svc.SetOpenAIResponseHeaderTimeoutSettings(context.Background(), &OpenAIResponseHeaderTimeoutSettings{
		Enabled: true,
		Seconds: 30,
	}))
	seconds, enabled := svc.GetOpenAIResponseHeaderTimeoutCached(context.Background())
	require.True(t, enabled)
	require.Equal(t, 30, seconds)

	stored, err := svc.GetOpenAIResponseHeaderTimeoutSettings(context.Background())
	require.NoError(t, err)
	require.True(t, stored.Enabled)
	require.Equal(t, 30, stored.Seconds)

	require.NoError(t, svc.SetOpenAIResponseHeaderTimeoutSettings(context.Background(), &OpenAIResponseHeaderTimeoutSettings{
		Enabled: false,
		Seconds: 30,
	}))
	seconds, enabled = svc.GetOpenAIResponseHeaderTimeoutCached(context.Background())
	require.False(t, enabled)
	require.Equal(t, 30, seconds)
}

func TestOpenAIResponseHeaderTimeoutSettingsValidation(t *testing.T) {
	svc := newOpenAIHeaderTimeoutTestService(t, 20)
	for _, seconds := range []int{0, -1, openAIResponseHeaderTimeoutMaxSeconds + 1} {
		require.Error(t, svc.SetOpenAIResponseHeaderTimeoutSettings(context.Background(), &OpenAIResponseHeaderTimeoutSettings{
			Enabled: true,
			Seconds: seconds,
		}))
	}
	for _, seconds := range []int{1, 600} {
		require.NoError(t, svc.SetOpenAIResponseHeaderTimeoutSettings(t.Context(), &OpenAIResponseHeaderTimeoutSettings{Enabled: true, Seconds: seconds}))
	}
}

type headerTimeoutBarrierRepo struct {
	panelRateLimitSettingRepo
	blockNext atomic.Bool
	started   chan struct{}
	release   chan struct{}
	readErr   error
}

func (r *headerTimeoutBarrierRepo) GetValue(ctx context.Context, key string) (string, error) {
	value, err := r.panelRateLimitSettingRepo.GetValue(ctx, key)
	if r.blockNext.Swap(false) {
		close(r.started) // Snapshot taken before the admin save.
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if r.readErr != nil {
			return "", r.readErr
		}
	}
	return value, err
}

func TestOpenAIResponseHeaderTimeoutStaleRefreshCannotUndoDisable(t *testing.T) {
	for _, readErr := range []error{nil, errors.New("old DB reader failed")} {
		t.Run(map[bool]string{true: "error", false: "success"}[readErr != nil], func(t *testing.T) {
			repo := &headerTimeoutBarrierRepo{
				panelRateLimitSettingRepo: panelRateLimitSettingRepo{values: map[string]string{}},
				started:                   make(chan struct{}), release: make(chan struct{}), readErr: readErr,
			}
			svc := NewSettingService(repo, &config.Config{})
			require.NoError(t, svc.SetOpenAIResponseHeaderTimeoutSettings(t.Context(), &OpenAIResponseHeaderTimeoutSettings{Enabled: true, Seconds: 20}))
			svc.storeOpenAIResponseHeaderTimeoutCache(OpenAIResponseHeaderTimeoutSettings{Enabled: true, Seconds: 20}, -time.Second)
			repo.blockNext.Store(true)
			done := make(chan OpenAIResponseHeaderTimeoutSettings, 1)
			go func() {
				seconds, enabled := svc.GetOpenAIResponseHeaderTimeoutCached(t.Context())
				done <- OpenAIResponseHeaderTimeoutSettings{Enabled: enabled, Seconds: seconds}
			}()
			select {
			case <-repo.started:
			case <-time.After(2 * time.Second):
				t.Fatal("old reader did not reach the snapshot barrier")
			}
			save := make(chan error, 1)
			go func() {
				save <- svc.SetOpenAIResponseHeaderTimeoutSettings(t.Context(), &OpenAIResponseHeaderTimeoutSettings{Enabled: false, Seconds: 20})
			}()
			select {
			case err := <-save:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				close(repo.release)
				t.Fatal("save must complete while the old reader is paused")
			}
			saved := svc.openAIResponseHeaderTimeoutCache.Load()
			close(repo.release)
			select {
			case settings := <-done:
				require.False(t, settings.Enabled, "old refresh must return the newer disable")
			case <-time.After(2 * time.Second):
				t.Fatal("old reader did not finish")
			}
			require.Same(t, saved, svc.openAIResponseHeaderTimeoutCache.Load(), "neither success nor error refresh may republish or shorten a newer save's TTL")
			for i := 0; i < 3; i++ {
				seconds, enabled := svc.GetOpenAIResponseHeaderTimeoutCached(t.Context())
				require.False(t, enabled)
				require.Equal(t, 20, seconds)
			}
		})
	}
}

func TestOpenAIResponseHeaderTimeoutStaticZeroAndDBErrors(t *testing.T) {
	cfg := &config.Config{}
	withoutDB := NewSettingService(nil, cfg)
	_, enabled := withoutDB.GetOpenAIResponseHeaderTimeoutCached(t.Context())
	require.False(t, enabled)
	repo := &panelRateLimitSettingRepo{getValueErr: errors.New("DB offline")}
	svc := NewSettingService(repo, cfg)
	_, enabled = svc.GetOpenAIResponseHeaderTimeoutCached(t.Context())
	require.False(t, enabled, "missing DB must keep explicit static 0 disabled")
	svc.storeOpenAIResponseHeaderTimeoutCache(OpenAIResponseHeaderTimeoutSettings{Enabled: false, Seconds: 33}, -time.Second)
	seconds, enabled := svc.GetOpenAIResponseHeaderTimeoutCached(t.Context())
	require.False(t, enabled)
	require.Equal(t, 33, seconds, "DB errors preserve last known settings")
}
