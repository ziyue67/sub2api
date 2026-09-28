package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type accountTokenGuardV2TestRepo struct {
	record      *AccountTokenGuardV2Record
	claimBusy   bool
	completion  *AccountTokenGuardV2ProbeCompletion
	rescheduled bool
}

func (r *accountTokenGuardV2TestRepo) UpsertAccount(_ context.Context, accountID int64, enabled, autoRelogin bool) error {
	if r.record == nil {
		r.record = &AccountTokenGuardV2Record{AccountID: accountID, NextProbeAt: time.Now()}
	}
	r.record.Enabled = enabled
	r.record.AutoReloginEnabled = autoRelogin
	return nil
}
func (r *accountTokenGuardV2TestRepo) DeleteAccount(context.Context, int64) error {
	r.record = nil
	return nil
}
func (r *accountTokenGuardV2TestRepo) GetAccount(context.Context, int64) (*AccountTokenGuardV2Record, error) {
	if r.record == nil {
		return nil, nil
	}
	copy := *r.record
	return &copy, nil
}
func (r *accountTokenGuardV2TestRepo) ListAccounts(context.Context) ([]AccountTokenGuardV2Record, error) {
	if r.record == nil {
		return nil, nil
	}
	return []AccountTokenGuardV2Record{*r.record}, nil
}
func (r *accountTokenGuardV2TestRepo) ClaimDue(context.Context, string, time.Duration, int) ([]AccountTokenGuardV2Record, error) {
	if r.record == nil || r.claimBusy {
		return nil, nil
	}
	return []AccountTokenGuardV2Record{*r.record}, nil
}
func (r *accountTokenGuardV2TestRepo) ClaimAccount(context.Context, int64, string, time.Duration) (*AccountTokenGuardV2Record, error) {
	if r.record == nil || r.claimBusy {
		return nil, nil
	}
	copy := *r.record
	return &copy, nil
}
func (r *accountTokenGuardV2TestRepo) CompleteProbe(_ context.Context, _ int64, _ string, result AccountTokenGuardV2ProbeCompletion) error {
	r.completion = &result
	if r.record != nil {
		r.record.ProbeState = result.ProbeState
		r.record.ProbeDetail = result.ProbeDetail
		r.record.FailStreak = result.FailStreak
		r.record.LastProbeAt = &result.LastProbeAt
		r.record.LastReauthAt = result.LastReauthAt
		r.record.NextProbeAt = result.NextProbeAt
		r.record.CooldownUntil = result.CooldownUntil
		r.record.BlockedReason = result.BlockedReason
	}
	return nil
}
func (r *accountTokenGuardV2TestRepo) MarkReauthRequested(context.Context, int64, time.Time, time.Time) error {
	return nil
}
func (r *accountTokenGuardV2TestRepo) RescheduleEnabled(context.Context) error {
	r.rescheduled = true
	return nil
}

type accountTokenGuardV2TestSettings struct{ raw string }

func (s *accountTokenGuardV2TestSettings) GetValue(context.Context, string) (string, error) {
	if s.raw == "" {
		return "", ErrSettingNotFound
	}
	return s.raw, nil
}
func (s *accountTokenGuardV2TestSettings) Set(_ context.Context, _ string, value string) error {
	s.raw = value
	return nil
}

type accountTokenGuardV2TestProber struct{ err error }

type accountTokenGuardV2BlockingRepo struct {
	accountTokenGuardV2TestRepo
	started  chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (r *accountTokenGuardV2BlockingRepo) ClaimDue(ctx context.Context, _ string, _ time.Duration, _ int) ([]AccountTokenGuardV2Record, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.finished)
	return nil, ctx.Err()
}

func TestAccountTokenGuardV2StopCancelsCycle(t *testing.T) {
	reauth, reader, _, _, _, _ := newReauthTestService("acct-1")
	repo := &accountTokenGuardV2BlockingRepo{started: make(chan struct{}), finished: make(chan struct{})}
	svc := NewAccountTokenGuardV2Service(repo, nil, reader, accountTokenGuardV2TestProber{}, reauth)
	svc.Start()
	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("cycle did not start")
	}
	svc.Stop()
	select {
	case <-repo.finished:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the in-flight cycle")
	}
	svc.Start()
	svc.Stop()
}

func (p accountTokenGuardV2TestProber) FetchOpenAIModelsList(context.Context, *Account) (*OpenAIModelsResponse, error) {
	return nil, p.err
}

func TestAccountTokenGuardV2SecondAuthFailureQueuesRelogin(t *testing.T) {
	reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
	require.NoError(t, reauthRepo.UpsertConfig(context.Background(), reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")))
	guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
		AccountID: 42, Enabled: true, AutoReloginEnabled: true, FailStreak: 1, NextProbeAt: time.Now(),
	}}
	svc := NewAccountTokenGuardV2Service(guardRepo, nil, reader, accountTokenGuardV2TestProber{err: &codexModelsManifestUpstreamError{
		err: errors.New("unauthorized"), statusCode: http.StatusUnauthorized,
	}}, reauth)
	record, err := guardRepo.GetAccount(context.Background(), 42)
	require.NoError(t, err)

	svc.runProbe(context.Background(), *record)

	require.NotNil(t, guardRepo.completion)
	require.Equal(t, AccountTokenGuardV2ProbeAuth, guardRepo.completion.ProbeState)
	require.Equal(t, 2, guardRepo.completion.FailStreak)
	require.NotNil(t, guardRepo.completion.LastReauthAt)
	require.NotNil(t, guardRepo.completion.CooldownUntil)
	require.Contains(t, guardRepo.completion.ProbeDetail, "automatic re-login queued")
	require.Equal(t, 1, reauthRepo.createCalls)
	require.NotNil(t, reauthRepo.task)
}

func TestAccountTokenGuardV2ActiveReloginTaskIsNotDuplicated(t *testing.T) {
	reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
	require.NoError(t, reauthRepo.UpsertConfig(context.Background(), reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")))
	existing := &OpenAIOAuthReauthTaskRecord{ID: 99, AccountID: 42, Status: OpenAIOAuthReauthStatusRunning, Stage: OpenAIOAuthReauthStageStarting}
	reauthRepo.task = existing
	reauthRepo.createErr = infraerrors.Conflict("OPENAI_REAUTH_TASK_ACTIVE", "task already active")
	guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
		AccountID: 42, Enabled: true, AutoReloginEnabled: true, FailStreak: 1, NextProbeAt: time.Now(),
	}}
	svc := NewAccountTokenGuardV2Service(guardRepo, nil, reader, accountTokenGuardV2TestProber{err: errors.New("invalid token")}, reauth)
	record, err := guardRepo.GetAccount(context.Background(), 42)
	require.NoError(t, err)

	svc.runProbe(context.Background(), *record)

	require.Same(t, existing, reauthRepo.task)
	require.Equal(t, 1, reauthRepo.createCalls)
	require.Contains(t, guardRepo.completion.ProbeDetail, "re-login task already active")
	require.Nil(t, guardRepo.completion.LastReauthAt)
}

func TestAccountTokenGuardV2ProbeNowDistinguishesMissingAndBusy(t *testing.T) {
	reauth, reader, _, _, _, _ := newReauthTestService("acct-1")
	prober := accountTokenGuardV2TestProber{}

	missing := NewAccountTokenGuardV2Service(&accountTokenGuardV2TestRepo{}, nil, reader, prober, reauth)
	_, err := missing.ProbeNow(context.Background(), 42)
	require.Equal(t, http.StatusNotFound, infraerrors.Code(err))

	busyRepo := &accountTokenGuardV2TestRepo{
		record:    &AccountTokenGuardV2Record{AccountID: 42, NextProbeAt: time.Now()},
		claimBusy: true,
	}
	busy := NewAccountTokenGuardV2Service(busyRepo, nil, reader, prober, reauth)
	_, err = busy.ProbeNow(context.Background(), 42)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
}

func TestAccountTokenGuardV2RulesDefaultAndPersistence(t *testing.T) {
	repo := &accountTokenGuardV2TestRepo{}
	settings := &accountTokenGuardV2TestSettings{}
	svc := NewAccountTokenGuardV2Service(repo, settings, nil, nil, nil)

	rules, err := svc.GetRules(context.Background())
	require.NoError(t, err)
	require.Equal(t, defaultAccountTokenGuardV2Rules(), rules)

	want := AccountTokenGuardV2Rules{
		ProbeIntervalSeconds: 900, RetryIntervalSeconds: 60,
		ReloginCooldownSeconds: 7200, FailStreakThreshold: 3,
	}
	saved, err := svc.SaveRules(context.Background(), want)
	require.NoError(t, err)
	require.Equal(t, want, saved)
	require.True(t, repo.rescheduled)

	var persisted AccountTokenGuardV2Rules
	require.NoError(t, json.Unmarshal([]byte(settings.raw), &persisted))
	require.Equal(t, want, persisted)
	reread, err := svc.GetRules(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, reread)
}

func TestAccountTokenGuardV2RulesRejectInvalidRange(t *testing.T) {
	repo := &accountTokenGuardV2TestRepo{}
	settings := &accountTokenGuardV2TestSettings{}
	svc := NewAccountTokenGuardV2Service(repo, settings, nil, nil, nil)

	_, err := svc.SaveRules(context.Background(), AccountTokenGuardV2Rules{
		ProbeIntervalSeconds: 59, RetryIntervalSeconds: 60,
		ReloginCooldownSeconds: 1800, FailStreakThreshold: 2,
	})
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Empty(t, settings.raw)
	require.False(t, repo.rescheduled)
}

func TestAccountTokenGuardV2InvalidSavedRulesDoNotQueueRelogin(t *testing.T) {
	reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
	record := AccountTokenGuardV2Record{AccountID: 42, Enabled: true, AutoReloginEnabled: true, FailStreak: 2}
	repo := &accountTokenGuardV2TestRepo{record: &record}
	settings := &accountTokenGuardV2TestSettings{raw: "invalid-json"}
	svc := NewAccountTokenGuardV2Service(repo, settings, reader, accountTokenGuardV2TestProber{err: errors.New("invalid token")}, reauth)
	svc.runProbe(context.Background(), record)
	require.Zero(t, reauthRepo.createCalls)
	require.Nil(t, repo.completion)
}

func TestAccountTokenGuardV2CustomRulesDriveProbe(t *testing.T) {
	reauth, reader, reauthRepo, _, _, _ := newReauthTestService("acct-1")
	require.NoError(t, reauthRepo.UpsertConfig(context.Background(), reauthEmailConfig(42, "user@example.com", "encrypted:https://mail.example.com/code")))
	guardRepo := &accountTokenGuardV2TestRepo{record: &AccountTokenGuardV2Record{
		AccountID: 42, Enabled: true, AutoReloginEnabled: true, NextProbeAt: time.Now(),
	}}
	settings := &accountTokenGuardV2TestSettings{}
	svc := NewAccountTokenGuardV2Service(guardRepo, settings, reader, accountTokenGuardV2TestProber{err: errors.New("invalid token")}, reauth)
	_, err := svc.SaveRules(context.Background(), AccountTokenGuardV2Rules{
		ProbeIntervalSeconds: 600, RetryIntervalSeconds: 45,
		ReloginCooldownSeconds: 7200, FailStreakThreshold: 1,
	})
	require.NoError(t, err)
	record, err := guardRepo.GetAccount(context.Background(), 42)
	require.NoError(t, err)

	svc.runProbe(context.Background(), *record)

	require.Equal(t, 1, reauthRepo.createCalls)
	require.WithinDuration(t, guardRepo.completion.LastProbeAt.Add(45*time.Second), guardRepo.completion.NextProbeAt, time.Second)
	require.NotNil(t, guardRepo.completion.CooldownUntil)
	require.WithinDuration(t, guardRepo.completion.LastProbeAt.Add(2*time.Hour), *guardRepo.completion.CooldownUntil, time.Second)
}
