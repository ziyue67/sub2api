package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/reauthruntime"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	AccountTokenGuardV2ProbePending   = "pending"
	AccountTokenGuardV2ProbeOK        = "ok"
	AccountTokenGuardV2ProbeAuth      = "auth"
	AccountTokenGuardV2ProbeTransient = "transient"

	AccountTokenGuardV2ProbeInterval   = 30 * time.Minute
	AccountTokenGuardV2RetryInterval   = 5 * time.Minute
	accountTokenGuardV2LeaseDuration   = 2 * time.Minute
	AccountTokenGuardV2ReloginCooldown = 30 * time.Minute
	AccountTokenGuardV2FailThreshold   = 2

	accountTokenGuardV2RulesSettingKey = "account_token_guard_v2_rules"
)

type AccountTokenGuardV2Rules struct {
	ProbeIntervalSeconds   int `json:"probe_interval_seconds"`
	RetryIntervalSeconds   int `json:"retry_interval_seconds"`
	ReloginCooldownSeconds int `json:"relogin_cooldown_seconds"`
	FailStreakThreshold    int `json:"fail_streak_threshold"`
}

func defaultAccountTokenGuardV2Rules() AccountTokenGuardV2Rules {
	return AccountTokenGuardV2Rules{
		ProbeIntervalSeconds:   int(AccountTokenGuardV2ProbeInterval.Seconds()),
		RetryIntervalSeconds:   int(AccountTokenGuardV2RetryInterval.Seconds()),
		ReloginCooldownSeconds: int(AccountTokenGuardV2ReloginCooldown.Seconds()),
		FailStreakThreshold:    AccountTokenGuardV2FailThreshold,
	}
}

func ValidateAccountTokenGuardV2Rules(rules AccountTokenGuardV2Rules) error {
	if rules.ProbeIntervalSeconds < 60 || rules.ProbeIntervalSeconds > 86400 {
		return errors.New("probe_interval_seconds must be between 60 and 86400")
	}
	if rules.RetryIntervalSeconds < 30 || rules.RetryIntervalSeconds > 86400 {
		return errors.New("retry_interval_seconds must be between 30 and 86400")
	}
	if rules.ReloginCooldownSeconds < 60 || rules.ReloginCooldownSeconds > 604800 {
		return errors.New("relogin_cooldown_seconds must be between 60 and 604800")
	}
	if rules.FailStreakThreshold < 1 || rules.FailStreakThreshold > 10 {
		return errors.New("fail_streak_threshold must be between 1 and 10")
	}
	return nil
}

type AccountTokenGuardV2Record struct {
	AccountID          int64
	Enabled            bool
	AutoReloginEnabled bool
	ProbeState         string
	ProbeDetail        string
	FailStreak         int
	LastProbeAt        *time.Time
	LastReauthAt       *time.Time
	NextProbeAt        time.Time
	CooldownUntil      *time.Time
	BlockedReason      string
	LeaseOwner         string
	LeaseUntil         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type AccountTokenGuardV2ProbeCompletion struct {
	ProbeState    string
	ProbeDetail   string
	FailStreak    int
	LastProbeAt   time.Time
	LastReauthAt  *time.Time
	NextProbeAt   time.Time
	CooldownUntil *time.Time
	BlockedReason string
}

type AccountTokenGuardV2Repository interface {
	UpsertAccount(ctx context.Context, accountID int64, enabled, autoRelogin bool) error
	DeleteAccount(ctx context.Context, accountID int64) error
	GetAccount(ctx context.Context, accountID int64) (*AccountTokenGuardV2Record, error)
	ListAccounts(ctx context.Context) ([]AccountTokenGuardV2Record, error)
	ClaimDue(ctx context.Context, owner string, leaseDuration time.Duration, limit int) ([]AccountTokenGuardV2Record, error)
	ClaimAccount(ctx context.Context, accountID int64, owner string, leaseDuration time.Duration) (*AccountTokenGuardV2Record, error)
	CompleteProbe(ctx context.Context, accountID int64, owner string, result AccountTokenGuardV2ProbeCompletion) error
	MarkReauthRequested(ctx context.Context, accountID int64, requestedAt, cooldownUntil time.Time) error
	RescheduleEnabled(ctx context.Context) error
}

type AccountTokenGuardV2Settings interface {
	GetValue(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

type AccountTokenGuardV2AccountReader interface {
	GetAccount(ctx context.Context, id int64) (*Account, error)
	GetProxy(ctx context.Context, id int64) (*Proxy, error)
}

type AccountTokenGuardV2Prober interface {
	FetchOpenAIModelsList(ctx context.Context, account *Account) (*OpenAIModelsResponse, error)
}

type AccountTokenGuardV2AccountInput struct {
	PreserveEnabled     bool
	PreserveAutoRelogin bool
	LoginEmail          string `json:"login_email"`
	CredentialMode      string `json:"credential_mode"`
	Engine              string `json:"engine"`
	ProxySource         string `json:"proxy_source"`
	ProxyID             *int64 `json:"proxy_id"`
	Password            string `json:"password"`
	TOTPSecret          string `json:"totp_secret"`
	OTPURL              string `json:"otp_url"`
	ClearPassword       bool   `json:"clear_password"`
	ClearTOTP           bool   `json:"clear_totp"`
	Enabled             bool   `json:"enabled"`
	AutoReloginEnabled  bool   `json:"auto_relogin_enabled"`
}

type AccountTokenGuardV2Account struct {
	AccountID          int64                    `json:"account_id"`
	AccountName        string                   `json:"account_name"`
	AccountStatus      string                   `json:"account_status"`
	Schedulable        bool                     `json:"schedulable"`
	Enabled            bool                     `json:"enabled"`
	AutoReloginEnabled bool                     `json:"auto_relogin_enabled"`
	ProbeState         string                   `json:"probe_state"`
	ProbeDetail        string                   `json:"probe_detail"`
	FailStreak         int                      `json:"fail_streak"`
	LastProbeAt        *time.Time               `json:"last_probe_at,omitempty"`
	LastReauthAt       *time.Time               `json:"last_reauth_at,omitempty"`
	NextProbeAt        time.Time                `json:"next_probe_at"`
	CooldownUntil      *time.Time               `json:"cooldown_until,omitempty"`
	BlockedReason      string                   `json:"blocked_reason,omitempty"`
	LoginConfig        *OpenAIOAuthReauthConfig `json:"login_config,omitempty"`
	LatestTask         *OpenAIOAuthReauthTask   `json:"latest_task,omitempty"`
}

type AccountTokenGuardV2Service struct {
	repo     AccountTokenGuardV2Repository
	settings AccountTokenGuardV2Settings
	accounts AccountTokenGuardV2AccountReader
	prober   AccountTokenGuardV2Prober
	reauth   *OpenAIOAuthReauthService

	leaseOwner string
	startOnce  sync.Once
	rootCtx    context.Context
	cancel     context.CancelFunc
	doneCh     chan struct{}
}

func NewAccountTokenGuardV2Service(repo AccountTokenGuardV2Repository, settings AccountTokenGuardV2Settings, accounts AccountTokenGuardV2AccountReader, prober AccountTokenGuardV2Prober, reauth *OpenAIOAuthReauthService) *AccountTokenGuardV2Service {
	host, _ := os.Hostname()
	ctx, cancel := context.WithCancel(context.Background())
	return &AccountTokenGuardV2Service{
		repo: repo, settings: settings, accounts: accounts, prober: prober, reauth: reauth,
		leaseOwner: fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()),
		rootCtx:    ctx, cancel: cancel, doneCh: make(chan struct{}),
	}
}

func (s *AccountTokenGuardV2Service) GetRules(ctx context.Context) (AccountTokenGuardV2Rules, error) {
	rules := defaultAccountTokenGuardV2Rules()
	if s == nil || s.settings == nil {
		return rules, nil
	}
	raw, err := s.settings.GetValue(ctx, accountTokenGuardV2RulesSettingKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return rules, err
	}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &rules); err != nil {
			return rules, err
		}
	}
	if err := ValidateAccountTokenGuardV2Rules(rules); err != nil {
		return rules, err
	}
	return rules, nil
}

func (s *AccountTokenGuardV2Service) SaveRules(ctx context.Context, rules AccountTokenGuardV2Rules) (AccountTokenGuardV2Rules, error) {
	if err := ValidateAccountTokenGuardV2Rules(rules); err != nil {
		return rules, infraerrors.BadRequest("TOKEN_GUARD_V2_RULES_INVALID", err.Error())
	}
	if s == nil || s.settings == nil {
		return rules, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_RULES_SAVE_FAILED", "settings repository is unavailable")
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return rules, err
	}
	if err := s.settings.Set(ctx, accountTokenGuardV2RulesSettingKey, string(raw)); err != nil {
		return rules, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_RULES_SAVE_FAILED", "failed to save inspection rules")
	}
	if err := s.repo.RescheduleEnabled(ctx); err != nil {
		return rules, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_RULES_RESCHEDULE_FAILED", "rules were saved but enabled accounts could not be rescheduled")
	}
	return rules, nil
}

func (s *AccountTokenGuardV2Service) Start() {
	if s == nil || s.repo == nil || s.accounts == nil || s.prober == nil || s.reauth == nil {
		return
	}
	s.startOnce.Do(func() {
		go func() {
			defer close(s.doneCh)
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			runCycle := func() {
				ctx, cancel := context.WithTimeout(s.rootCtx, 90*time.Second)
				defer cancel()
				if rows, err := s.repo.ListAccounts(ctx); err == nil && len(rows) > 0 {
					s.reauth.EnsureWorker()
				}
				if _, err := s.RunDue(ctx); err != nil {
					slog.Warn("account_token_guard_v2_cycle_failed", "error", err)
				}
			}
			if s.rootCtx.Err() != nil {
				return
			}
			runCycle()
			for {
				select {
				case <-s.rootCtx.Done():
					return
				case <-ticker.C:
					runCycle()
				}
			}
		}()
	})
}

func (s *AccountTokenGuardV2Service) Stop() {
	if s == nil {
		return
	}
	s.reauth.stopWorker()
	s.cancel()
	s.startOnce.Do(func() { close(s.doneCh) })
	select {
	case <-s.doneCh:
	case <-time.After(10 * time.Second):
	}
}

func (s *AccountTokenGuardV2Service) SaveAccount(ctx context.Context, accountID int64, input AccountTokenGuardV2AccountInput) (*AccountTokenGuardV2Account, error) {
	if accountID <= 0 {
		return nil, infraerrors.BadRequest("TOKEN_GUARD_V2_ACCOUNT_INVALID", "invalid account id")
	}
	preserveSwitches := false
	if input.PreserveEnabled || input.PreserveAutoRelogin {
		existing, err := s.repo.GetAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			preserveSwitches = input.PreserveEnabled && input.PreserveAutoRelogin
			if input.PreserveEnabled {
				input.Enabled = existing.Enabled
			}
			if input.PreserveAutoRelogin {
				input.AutoReloginEnabled = existing.AutoReloginEnabled
			}
		}
	}
	if _, err := s.reauth.SaveCredentialConfig(ctx, accountID, OpenAIOAuthReauthConfigInput{
		LoginEmail: input.LoginEmail, CredentialMode: input.CredentialMode, Engine: input.Engine,
		ProxySource: input.ProxySource, ProxyID: input.ProxyID,
		Password: input.Password, TOTPSecret: input.TOTPSecret, OTPURL: input.OTPURL,
		ClearPassword: input.ClearPassword, ClearTOTP: input.ClearTOTP,
	}); err != nil {
		return nil, err
	}
	if !preserveSwitches {
		if err := s.repo.UpsertAccount(ctx, accountID, input.Enabled, input.AutoReloginEnabled); err != nil {
			return nil, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_SAVE_FAILED", "failed to save monitored account")
		}
	}
	return s.accountView(ctx, accountID)
}

func (s *AccountTokenGuardV2Service) RemoveAccount(ctx context.Context, accountID int64) error {
	if err := s.repo.DeleteAccount(ctx, accountID); err != nil {
		return infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_DELETE_FAILED", "failed to remove monitored account")
	}
	return nil
}

func (s *AccountTokenGuardV2Service) ListAccounts(ctx context.Context) ([]AccountTokenGuardV2Account, error) {
	records, err := s.repo.ListAccounts(ctx)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_LIST_FAILED", "failed to list monitored accounts")
	}
	result := make([]AccountTokenGuardV2Account, 0, len(records))
	for _, record := range records {
		view, viewErr := s.recordView(ctx, record)
		if viewErr != nil {
			view = &AccountTokenGuardV2Account{
				AccountID: record.AccountID, Enabled: record.Enabled, AutoReloginEnabled: record.AutoReloginEnabled,
				ProbeState: record.ProbeState, ProbeDetail: record.ProbeDetail, FailStreak: record.FailStreak,
				LastProbeAt: record.LastProbeAt, LastReauthAt: record.LastReauthAt, NextProbeAt: record.NextProbeAt,
				CooldownUntil: record.CooldownUntil, BlockedReason: "account or login configuration is unavailable",
			}
		}
		result = append(result, *view)
	}
	return result, nil
}

func (s *AccountTokenGuardV2Service) accountView(ctx context.Context, accountID int64) (*AccountTokenGuardV2Account, error) {
	record, err := s.repo.GetAccount(ctx, accountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_READ_FAILED", "failed to read monitored account")
	}
	if record == nil {
		return nil, infraerrors.New(http.StatusNotFound, "TOKEN_GUARD_V2_NOT_FOUND", "monitored account not found")
	}
	return s.recordView(ctx, *record)
}

func (s *AccountTokenGuardV2Service) recordView(ctx context.Context, record AccountTokenGuardV2Record) (*AccountTokenGuardV2Account, error) {
	account, err := s.accounts.GetAccount(ctx, record.AccountID)
	if err != nil || account == nil {
		return nil, errors.New("account not found")
	}
	reauthStatus, err := s.reauth.GetStatus(ctx, record.AccountID)
	if err != nil {
		return nil, err
	}
	lastReauthAt := record.LastReauthAt
	if reauthStatus.Task != nil && reauthStatus.Task.Status == OpenAIOAuthReauthStatusSucceeded && reauthStatus.Task.FinishedAt != nil {
		lastReauthAt = reauthStatus.Task.FinishedAt
	}
	return &AccountTokenGuardV2Account{
		AccountID: record.AccountID, AccountName: account.Name, AccountStatus: account.Status, Schedulable: account.Schedulable,
		Enabled: record.Enabled, AutoReloginEnabled: record.AutoReloginEnabled,
		ProbeState: record.ProbeState, ProbeDetail: record.ProbeDetail, FailStreak: record.FailStreak,
		LastProbeAt: record.LastProbeAt, LastReauthAt: lastReauthAt, NextProbeAt: record.NextProbeAt,
		CooldownUntil: record.CooldownUntil, BlockedReason: record.BlockedReason,
		LoginConfig: reauthStatus.Config, LatestTask: reauthStatus.Task,
	}, nil
}

func (s *AccountTokenGuardV2Service) RunDue(ctx context.Context) (int, error) {
	records, err := s.repo.ClaimDue(ctx, s.leaseOwner, accountTokenGuardV2LeaseDuration, 10)
	if err != nil {
		return 0, err
	}
	for i := range records {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		s.runProbe(ctx, records[i])
	}
	return len(records), nil
}

func (s *AccountTokenGuardV2Service) ProbeNow(ctx context.Context, accountID int64) (*AccountTokenGuardV2Account, error) {
	existing, err := s.repo.GetAccount(ctx, accountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_READ_FAILED", "failed to read monitored account")
	}
	if existing == nil {
		return nil, infraerrors.New(http.StatusNotFound, "TOKEN_GUARD_V2_NOT_FOUND", "monitored account not found")
	}
	record, err := s.repo.ClaimAccount(ctx, accountID, s.leaseOwner, accountTokenGuardV2LeaseDuration)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_CLAIM_FAILED", "failed to start account inspection")
	}
	if record == nil {
		return nil, infraerrors.Conflict("TOKEN_GUARD_V2_PROBE_BUSY", "account inspection is already running")
	}
	s.runProbe(ctx, *record)
	return s.accountView(ctx, accountID)
}

func (s *AccountTokenGuardV2Service) ReloginNow(ctx context.Context, accountID int64) (*OpenAIOAuthReauthTask, error) {
	if record, err := s.repo.GetAccount(ctx, accountID); err != nil || record == nil {
		return nil, infraerrors.New(http.StatusNotFound, "TOKEN_GUARD_V2_NOT_FOUND", "monitored account not found")
	}
	rules, err := s.GetRules(ctx)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "TOKEN_GUARD_V2_RULES_READ_FAILED", "failed to read inspection rules")
	}
	task, err := s.reauth.CreateTask(ctx, accountID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	_ = s.repo.MarkReauthRequested(ctx, accountID, now, now.Add(time.Duration(rules.ReloginCooldownSeconds)*time.Second))
	return task, nil
}

func (s *AccountTokenGuardV2Service) runProbe(ctx context.Context, record AccountTokenGuardV2Record) {
	rules, err := s.GetRules(ctx)
	if err != nil {
		slog.Warn("account_token_guard_v2_rules_read_failed", "error", err)
		return
	}
	now := time.Now()
	completion := AccountTokenGuardV2ProbeCompletion{
		ProbeState: AccountTokenGuardV2ProbeTransient, ProbeDetail: "inspection failed",
		FailStreak: record.FailStreak, LastProbeAt: now, NextProbeAt: now.Add(time.Duration(rules.ProbeIntervalSeconds) * time.Second),
		CooldownUntil: record.CooldownUntil,
	}
	account, err := s.accounts.GetAccount(ctx, record.AccountID)
	if err != nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsCredentialShadow() || account.IsOpenAIPersonalAccessToken() || account.IsOpenAIAgentIdentity() {
		completion.ProbeDetail = "account is no longer an eligible OpenAI OAuth parent account"
		completion.BlockedReason = completion.ProbeDetail
		s.completeProbe(ctx, record.AccountID, completion)
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	state, detail := s.probeAccount(probeCtx, account)
	cancel()
	completion.ProbeState = state
	completion.ProbeDetail = detail
	completion.BlockedReason = ""
	switch state {
	case AccountTokenGuardV2ProbeOK:
		completion.FailStreak = 0
		completion.CooldownUntil = nil
	case AccountTokenGuardV2ProbeAuth:
		completion.FailStreak++
		completion.NextProbeAt = now.Add(time.Duration(rules.RetryIntervalSeconds) * time.Second)
		cooldownActive := record.CooldownUntil != nil && record.CooldownUntil.After(now)
		if record.AutoReloginEnabled && completion.FailStreak >= rules.FailStreakThreshold && !cooldownActive {
			task, taskErr := s.reauth.CreateTask(ctx, record.AccountID)
			if taskErr == nil && task != nil {
				completion.LastReauthAt = &now
				cooldown := now.Add(time.Duration(rules.ReloginCooldownSeconds) * time.Second)
				completion.CooldownUntil = &cooldown
				completion.ProbeDetail = detail + "; automatic re-login queued"
			} else if infraerrors.Code(taskErr) == http.StatusConflict {
				completion.ProbeDetail = detail + "; re-login task already active"
			} else if taskErr != nil {
				completion.BlockedReason = "automatic re-login could not be queued"
				completion.ProbeDetail = detail + "; " + completion.BlockedReason
			}
		}
	}
	s.completeProbe(ctx, record.AccountID, completion)
}

func (s *AccountTokenGuardV2Service) completeProbe(ctx context.Context, accountID int64, completion AccountTokenGuardV2ProbeCompletion) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.repo.CompleteProbe(persistCtx, accountID, s.leaseOwner, completion); err != nil {
		slog.Warn("account_token_guard_v2_probe_save_failed", "account_id", accountID, "error", err)
	}
}

func (s *AccountTokenGuardV2Service) probeAccount(ctx context.Context, account *Account) (string, string) {
	if strings.TrimSpace(account.GetCredential("access_token")) == "" {
		return AccountTokenGuardV2ProbeAuth, "account has no access token"
	}
	_, err := s.prober.FetchOpenAIModelsList(ctx, account)
	if err == nil {
		return AccountTokenGuardV2ProbeOK, "credential accepted"
	}
	var upstreamErr *codexModelsManifestUpstreamError
	if errors.As(err, &upstreamErr) && (upstreamErr.statusCode == http.StatusUnauthorized || upstreamErr.statusCode == http.StatusForbidden) {
		return AccountTokenGuardV2ProbeAuth, fmt.Sprintf("upstream returned %d", upstreamErr.statusCode)
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"unauthorized", "invalid token", "invalid_token", "token expired", "invalid_grant", "requires re-login"} {
		if strings.Contains(text, marker) {
			return AccountTokenGuardV2ProbeAuth, "credential was rejected"
		}
	}
	return AccountTokenGuardV2ProbeTransient, "temporary inspection failure"
}

// WorkerStatus separates saved login credentials from execution availability.
func (s *AccountTokenGuardV2Service) WorkerStatus() reauthruntime.Status {
	return s.reauth.WorkerStatus()
}
