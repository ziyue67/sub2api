package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type reauthRuntimeTestSettings struct {
	values map[string]string
	fail   bool
}

func (s *reauthRuntimeTestSettings) GetValue(_ context.Context, key string) (string, error) {
	if s.fail {
		return "", errors.New("database unavailable")
	}
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (s *reauthRuntimeTestSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func (r *reauthTestRepo) ClaimNextTaskForRuntime(ctx context.Context, worker string, stale time.Duration, mode string, engines []string, global string) (*OpenAIOAuthReauthTaskRecord, error) {
	if r.config == nil || (mode != "" && r.config.CredentialMode != mode) {
		return nil, nil
	}
	effective := effectiveReauthEngine(r.config.CredentialMode, r.config.Engine, global)
	for _, engine := range engines {
		if engine == effective {
			return r.ClaimNextTask(ctx, worker, stale)
		}
	}
	return nil, nil
}

func TestOpenAIOAuthReauthRuntimeSwitchUsesGlobalEngineForExistingAndNewAccounts(t *testing.T) {
	ctx := context.Background()
	svc, _, repo, _, _, _ := newReauthTestService("acct-1")
	svc.settings = &reauthRuntimeTestSettings{values: map[string]string{}}
	input := OpenAIOAuthReauthConfigInput{LoginEmail: "user@example.com", CredentialMode: OpenAIOAuthReauthModePasswordTOTP, Password: "synthetic-password"}
	_, err := svc.SaveCredentialConfig(ctx, 42, input)
	require.NoError(t, err)
	_, err = svc.CreateTask(ctx, 42)
	require.NoError(t, err)
	_, err = svc.SaveRuntimeSettings(ctx, OpenAIOAuthReauthRuntimeSettings{Engine: OpenAIOAuthReauthEngineSessionStudio, WorkerConcurrency: 5})
	require.NoError(t, err)
	claim, err := svc.ClaimTask(ctx, "old-worker")
	require.NoError(t, err)
	require.Nil(t, claim)
	require.Equal(t, OpenAIOAuthReauthStatusQueued, repo.task.Status)
	claim, err = svc.ClaimTaskWithEngines(ctx, "new-worker", []string{OpenAIOAuthReauthEngineSessionStudio})
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, OpenAIOAuthReauthEngineSessionStudio, claim.Engine)
	require.Equal(t, "synthetic-password", claim.Password)
	_, err = svc.SaveRuntimeSettings(ctx, OpenAIOAuthReauthRuntimeSettings{Engine: OpenAIOAuthReauthEngineLocal, WorkerConcurrency: 1})
	require.NoError(t, err)
	require.Equal(t, OpenAIOAuthReauthEngineSessionStudio, claim.Engine, "running claim is immutable")
	input.Engine = OpenAIOAuthReauthEngineSessionStudio
	view, err := svc.SaveCredentialConfig(ctx, 42, input)
	require.NoError(t, err)
	require.Equal(t, OpenAIOAuthReauthEngineLocal, view.Engine, "legacy clients cannot override the global selection")
}

func TestOpenAIOAuthReauthRuntimeSettingsPreserveLegacyAndRejectInvalidValues(t *testing.T) {
	svc, _, _, _, _, _ := newReauthTestService("acct-1")
	settings := &reauthRuntimeTestSettings{values: map[string]string{}}
	svc.settings = settings
	ctx := context.Background()
	initial, err := svc.GetRuntimeSettings(ctx)
	require.NoError(t, err)
	require.Empty(t, initial.Engine)
	_, err = svc.SaveRuntimeSettings(ctx, OpenAIOAuthReauthRuntimeSettings{WorkerConcurrency: 4})
	require.NoError(t, err)
	require.Equal(t, OpenAIOAuthReauthEngineSessionStudio, effectiveReauthEngine("password_totp", "session_studio", ""))
	require.Equal(t, OpenAIOAuthReauthEngineLocal, effectiveReauthEngine("email_otp_url", "local_worker", "session_studio"))
	for _, cfg := range []OpenAIOAuthReauthRuntimeSettings{{Engine: "invalid", WorkerConcurrency: 3}, {WorkerConcurrency: 0}, {WorkerConcurrency: 17}} {
		_, err := svc.SaveRuntimeSettings(ctx, cfg)
		require.Error(t, err)
	}
	settings.fail = true
	_, err = svc.ClaimTask(ctx, "worker")
	require.Error(t, err)
}

func TestAccountTokenGuardV2CredentialEditPreservesAutomation(t *testing.T) {
	reauth, reader, _, _, _, _ := newReauthTestService("acct-1")
	repo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{AccountID: 42, Enabled: false, AutoReloginEnabled: false}}
	svc := NewAccountTokenGuardV2Service(repo, nil, reader, nil, reauth)
	_, err := svc.SaveAccount(context.Background(), 42, AccountTokenGuardV2AccountInput{
		LoginEmail: "user@example.com", CredentialMode: "password_totp", Password: "synthetic", Enabled: true, AutoReloginEnabled: true, PreserveEnabled: true, PreserveAutoRelogin: true,
	})
	require.NoError(t, err)
	require.False(t, repo.record.Enabled)
	require.False(t, repo.record.AutoReloginEnabled)
}

func (r *accountTokenGuardV2TestRepo) UpdateSwitches(_ context.Context, id int64, enabled, autoRelogin *bool) error {
	if r.record == nil || r.record.AccountID != id {
		return errors.New("account not found")
	}
	if enabled != nil {
		r.record.Enabled = *enabled
	}
	if autoRelogin != nil {
		r.record.AutoReloginEnabled = *autoRelogin
	}
	return nil
}

func TestAccountTokenGuardV2SwitchDoesNotReadOrSaveCredentials(t *testing.T) {
	repo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{AccountID: 42, Enabled: true, AutoReloginEnabled: true}}
	// No credential reader/encryptor/reauth service: switching must still work.
	svc := NewAccountTokenGuardV2Service(repo, nil, nil, nil, nil)
	disabled := false
	saved, err := svc.UpdateSwitches(context.Background(), 42, &disabled, nil)
	require.NoError(t, err)
	require.False(t, saved.Enabled)
	require.True(t, saved.AutoReloginEnabled)
	saved, err = svc.UpdateSwitches(context.Background(), 42, nil, &disabled)
	require.NoError(t, err)
	require.False(t, saved.Enabled)
	require.False(t, saved.AutoReloginEnabled)
	_, err = svc.UpdateSwitches(context.Background(), 42, nil, nil)
	require.Error(t, err)
}

func TestOpenAIOAuthReauthRuntimeRejectsCorruptSettingsAndPreservesWorkerEnv(t *testing.T) {
	svc, _, _, _, _, _ := newReauthTestService("acct-1")
	settings := &reauthRuntimeTestSettings{values: map[string]string{}}
	svc.settings = settings
	t.Setenv("OPENAI_REAUTH_CONCURRENCY", "6")
	cfg, err := svc.GetRuntimeSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 6, cfg.WorkerConcurrency)
	require.False(t, cfg.ConcurrencyConfigured)
	for _, raw := range []string{"{bad", "null", "{\"worker_concurrency\":null}", "{\"worker_concurrency\":17}"} {
		settings.values[openAIOAuthReauthRuntimeSettingsKey] = raw
		_, err = svc.GetRuntimeSettings(context.Background())
		require.Error(t, err)
	}
	settings.values[openAIOAuthReauthRuntimeSettingsKey] = "{\"engine\":\"\",\"worker_concurrency\":2}"
	cfg, err = svc.GetRuntimeSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, cfg.WorkerConcurrency)
	require.True(t, cfg.ConcurrencyConfigured)
}
