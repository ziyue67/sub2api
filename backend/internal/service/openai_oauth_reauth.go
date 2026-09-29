package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	OpenAIOAuthReauthStatusQueued             = "queued"
	OpenAIOAuthReauthStatusRunning            = "running"
	OpenAIOAuthReauthStatusCallbackProcessing = "callback_processing"
	OpenAIOAuthReauthStatusSucceeded          = "succeeded"
	OpenAIOAuthReauthStatusFailed             = "failed"

	OpenAIOAuthReauthStageQueued              = "queued"
	OpenAIOAuthReauthStageStarting            = "starting"
	OpenAIOAuthReauthStageProtocolConnecting  = "protocol_connecting"
	OpenAIOAuthReauthStageEmailSubmitted      = "email_submitted"
	OpenAIOAuthReauthStageWaitingOTP          = "waiting_otp"
	OpenAIOAuthReauthStageOTPSubmitted        = "otp_submitted"
	OpenAIOAuthReauthStagePasswordSubmitted   = "password_submitted"
	OpenAIOAuthReauthStageMFASubmitted        = "mfa_submitted"
	OpenAIOAuthReauthStageWaitingCallback     = "waiting_callback"
	OpenAIOAuthReauthStageExchangingToken     = "exchanging_token"
	OpenAIOAuthReauthStageApplyingCredentials = "applying_credentials"
	OpenAIOAuthReauthStageSucceeded           = "succeeded"
	OpenAIOAuthReauthStageFailed              = "failed"
)

const (
	OpenAIOAuthReauthModeEmailOTPURL  = "email_otp_url"
	OpenAIOAuthReauthModePasswordTOTP = "password_totp"
)

const (
	OpenAIOAuthReauthProxySourceAccount      = "account"
	OpenAIOAuthReauthProxySourceManagedProxy = "managed_proxy"
	OpenAIOAuthReauthProxySourceMihomo       = "mihomo"
)

const (
	openAIOAuthReauthStaleAfter = 30 * time.Minute
	maxOpenAIOAuthReauthURL     = 4096
	maxOpenAIOAuthReauthError   = 500
)

// OpenAIOAuthReauthConfig is the non-secret representation of a saved re-login
// configuration. Secret values are encrypted at rest and never returned by the
// admin API.
type OpenAIOAuthReauthConfig struct {
	AccountID          int64     `json:"account_id"`
	LoginEmail         string    `json:"login_email"`
	CredentialMode     string    `json:"credential_mode"`
	ProxySource        string    `json:"proxy_source"`
	ProxyID            *int64    `json:"proxy_id,omitempty"`
	URLMasked          string    `json:"otp_url_masked,omitempty"`
	PasswordConfigured bool      `json:"password_configured"`
	TOTPConfigured     bool      `json:"totp_configured"`
	Configured         bool      `json:"configured"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

type OpenAIOAuthReauthConfigInput struct {
	LoginEmail     string
	CredentialMode string
	ProxySource    string
	ProxyID        *int64
	Password       string
	TOTPSecret     string
	OTPURL         string
	ClearPassword  bool
	ClearTOTP      bool
	PreserveProxy  bool
}

// OpenAIOAuthReauthTask is the safe task status exposed to administrators.
type OpenAIOAuthReauthTask struct {
	ID         int64      `json:"id"`
	AccountID  int64      `json:"account_id"`
	Status     string     `json:"status"`
	Stage      string     `json:"stage"`
	Error      string     `json:"error,omitempty"`
	Attempt    int        `json:"attempt"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// OpenAIOAuthReauthStatus combines saved configuration and the latest task.
type OpenAIOAuthReauthStatus struct {
	Config *OpenAIOAuthReauthConfig `json:"config,omitempty"`
	Task   *OpenAIOAuthReauthTask   `json:"task,omitempty"`
}

// OpenAIOAuthReauthRepository is deliberately small so the task state can be
// tested without a live database. The production implementation uses raw SQL
// because the task table is intentionally outside the generated Ent schema.
type OpenAIOAuthReauthRepository interface {
	UpsertConfig(ctx context.Context, config *OpenAIOAuthReauthStoredConfig) error
	GetConfig(ctx context.Context, accountID int64) (*OpenAIOAuthReauthStoredConfig, error)
	CreateTask(ctx context.Context, accountID int64, expectedCredentialsHash string) (*OpenAIOAuthReauthTaskRecord, error)
	GetLatestTask(ctx context.Context, accountID int64) (*OpenAIOAuthReauthTaskRecord, error)
	GetTask(ctx context.Context, taskID int64) (*OpenAIOAuthReauthTaskRecord, error)
	ClaimNextTask(ctx context.Context, workerID string, staleAfter time.Duration) (*OpenAIOAuthReauthTaskRecord, error)
	SetSession(ctx context.Context, taskID int64, workerID, sessionID string) error
	UpdateStage(ctx context.Context, taskID int64, workerID, stage string) error
	BeginCallback(ctx context.Context, taskID int64, workerID string) (*OpenAIOAuthReauthTaskRecord, bool, error)
	BeginDirectCallback(ctx context.Context, taskID int64, workerID string) (*OpenAIOAuthReauthTaskRecord, bool, error)
	MarkSucceeded(ctx context.Context, taskID int64, workerID string) error
	MarkFailed(ctx context.Context, taskID int64, workerID, reason string) error
}

type OpenAIOAuthReauthStoredConfig struct {
	AccountID            int64
	LoginEmail           string
	CredentialMode       string
	ProxySource          string
	ProxyID              *int64
	PasswordCiphertext   string
	TOTPSecretCiphertext string
	OTPURLCiphertext     string
	UpdatedAt            time.Time
}

// OpenAIOAuthReauthTaskRecord contains fields needed by the worker protocol.
// It is never serialized directly to an administrator response.
type OpenAIOAuthReauthTaskRecord struct {
	ID                      int64
	AccountID               int64
	Status                  string
	Stage                   string
	WorkerID                string
	AuthSessionID           string
	ExpectedCredentialsHash string
	Error                   string
	Attempt                 int
	CreatedAt               time.Time
	UpdatedAt               time.Time
	FinishedAt              *time.Time
}

// OpenAIOAuthReauthCredentialUpdater is implemented by the account repository
// in production. It performs the credential swap with an expected-value guard
// so a concurrent admin edit cannot be overwritten by a stale re-auth task.
type OpenAIOAuthReauthCredentialUpdater interface {
	ApplyOpenAIOAuthReauth(ctx context.Context, taskID int64, workerID string, accountID int64, expectedCredentials, credentials, extra map[string]any) (bool, error)
}

type OpenAIOAuthReauthAccountReader interface {
	GetAccount(ctx context.Context, id int64) (*Account, error)
	GetProxy(ctx context.Context, id int64) (*Proxy, error)
}

type OpenAIOAuthReauthService struct {
	repo                    OpenAIOAuthReauthRepository
	accounts                OpenAIOAuthReauthAccountReader
	credentialUpdater       OpenAIOAuthReauthCredentialUpdater
	oauth                   *OpenAIOAuthService
	encryptor               SecretEncryptor
	encryptionKeyConfigured bool
	tokenCacheInvalidator   TokenCacheInvalidator
	runtimeBlocker          AccountRuntimeBlocker
	acquireMihomoProxy      func(context.Context, string) (string, func(), error)
	mihomoLeaseMu           sync.Mutex
	mihomoLeases            map[int64]func()
}

func NewOpenAIOAuthReauthService(
	repo OpenAIOAuthReauthRepository,
	accounts OpenAIOAuthReauthAccountReader,
	credentialUpdater OpenAIOAuthReauthCredentialUpdater,
	oauth *OpenAIOAuthService,
	encryptor SecretEncryptor,
	encryptionKeyConfigured bool,
	tokenCacheInvalidator TokenCacheInvalidator,
	runtimeBlocker AccountRuntimeBlocker,
) *OpenAIOAuthReauthService {
	return &OpenAIOAuthReauthService{
		repo:                    repo,
		accounts:                accounts,
		credentialUpdater:       credentialUpdater,
		oauth:                   oauth,
		encryptor:               encryptor,
		encryptionKeyConfigured: encryptionKeyConfigured,
		tokenCacheInvalidator:   tokenCacheInvalidator,
		runtimeBlocker:          runtimeBlocker,
		mihomoLeases:            make(map[int64]func()),
		acquireMihomoProxy: func(ctx context.Context, scope string) (string, func(), error) {
			lease, err := mihomo.AcquireBPSTransientLease(ctx, scope)
			if err != nil {
				return "", nil, err
			}
			return lease.ProxyURL, lease.Release, nil
		},
	}
}

func (s *OpenAIOAuthReauthService) replaceMihomoLease(taskID int64, release func()) {
	if release == nil {
		return
	}
	s.mihomoLeaseMu.Lock()
	previous := s.mihomoLeases[taskID]
	s.mihomoLeases[taskID] = release
	s.mihomoLeaseMu.Unlock()
	if previous != nil {
		previous()
	}
}

func (s *OpenAIOAuthReauthService) releaseMihomoLease(taskID int64) {
	s.mihomoLeaseMu.Lock()
	release := s.mihomoLeases[taskID]
	delete(s.mihomoLeases, taskID)
	s.mihomoLeaseMu.Unlock()
	if release != nil {
		release()
	}
}

func normalizeReauthProxySource(source string, proxyID *int64) (string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		if proxyID != nil {
			source = OpenAIOAuthReauthProxySourceManagedProxy
		} else {
			source = OpenAIOAuthReauthProxySourceAccount
		}
	}
	switch source {
	case OpenAIOAuthReauthProxySourceAccount, OpenAIOAuthReauthProxySourceMihomo:
		if proxyID != nil {
			return "", errors.New("proxy id is only valid for managed_proxy")
		}
	case OpenAIOAuthReauthProxySourceManagedProxy:
		if proxyID == nil || *proxyID <= 0 {
			return "", errors.New("managed_proxy requires a proxy id")
		}
	default:
		return "", errors.New("invalid proxy source")
	}
	return source, nil
}

func normalizeReauthEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > 320 {
		return "", infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_EMAIL_INVALID", "a valid login email is required")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) || !strings.Contains(email, "@") {
		return "", infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_EMAIL_INVALID", "a valid login email is required")
	}
	return email, nil
}

// ValidateOpenAIOAuthReauthURL accepts HTTPS endpoints. Plain HTTP is only
// allowed for loopback addresses so local fake-mail tests remain possible
// without turning the saved mailbox URL into an unrestricted SSRF primitive.
func ValidateOpenAIOAuthReauthURL(raw string) (*url.URL, error) {
	if len(strings.TrimSpace(raw)) == 0 || len(raw) > maxOpenAIOAuthReauthURL {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_OTP_URL_INVALID", "a valid OTP API URL is required")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_OTP_URL_INVALID", "OTP API URL must be an absolute URL without credentials or fragments")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	allowLocalHTTP := scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
	if scheme != "https" && !allowLocalHTTP {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_OTP_URL_INVALID", "OTP API URL must use HTTPS (HTTP is allowed only for localhost tests)")
	}
	return u, nil
}

func MaskOpenAIOAuthReauthURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "configured"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

func (s *OpenAIOAuthReauthService) ensureReady() error {
	if s == nil || s.repo == nil || s.accounts == nil || s.credentialUpdater == nil || s.oauth == nil || s.encryptor == nil {
		return infraerrors.New(http.StatusServiceUnavailable, "OPENAI_REAUTH_UNAVAILABLE", "OpenAI automatic re-login is not available")
	}
	return nil
}

func (s *OpenAIOAuthReauthService) ensureDurableEncryption() error {
	status, err := s.CredentialEncryptionStatus()
	if err != nil {
		return err
	}
	if !status.Configured {
		return infraerrors.New(
			http.StatusBadRequest,
			"OPENAI_REAUTH_ENCRYPTION_KEY_REQUIRED",
			"Enable credential encryption in Credential Operations before saving automatic re-login credentials",
		)
	}
	return nil
}

func (s *OpenAIOAuthReauthService) accountFor(ctx context.Context, accountID int64) (*Account, error) {
	if accountID <= 0 {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_ACCOUNT_INVALID", "invalid account id")
	}
	account, err := s.accounts.GetAccount(ctx, accountID)
	if err != nil || account == nil {
		return nil, infraerrors.New(http.StatusNotFound, "OPENAI_REAUTH_ACCOUNT_NOT_FOUND", "account not found")
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsCredentialShadow() ||
		account.IsOpenAIPersonalAccessToken() || account.IsOpenAIAgentIdentity() {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_ACCOUNT_INVALID", "only an OpenAI OAuth parent account can be re-authenticated")
	}
	return account, nil
}

func (s *OpenAIOAuthReauthService) reauthProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if *proxyID <= 0 {
		return "", errors.New("invalid proxy id")
	}
	proxy, err := s.accounts.GetProxy(ctx, *proxyID)
	if err != nil || proxy == nil || !proxy.IsActive() || proxy.IsExpired(time.Now()) || strings.TrimSpace(proxy.URL()) == "" {
		return "", errors.New("proxy is unavailable")
	}
	return proxy.URL(), nil
}

func (s *OpenAIOAuthReauthService) SaveConfig(ctx context.Context, accountID int64, loginEmail, otpURL string) (*OpenAIOAuthReauthConfig, error) {
	return s.SaveCredentialConfig(ctx, accountID, OpenAIOAuthReauthConfigInput{
		LoginEmail: loginEmail, CredentialMode: OpenAIOAuthReauthModeEmailOTPURL, OTPURL: otpURL, PreserveProxy: true,
	})
}

func (s *OpenAIOAuthReauthService) SaveCredentialConfig(ctx context.Context, accountID int64, input OpenAIOAuthReauthConfigInput) (*OpenAIOAuthReauthConfig, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if err := s.ensureDurableEncryption(); err != nil {
		return nil, err
	}
	if _, err := s.accountFor(ctx, accountID); err != nil {
		return nil, err
	}
	existing, err := s.repo.GetConfig(ctx, accountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_READ_FAILED", "failed to read re-login configuration")
	}
	proxySource := input.ProxySource
	proxyID := input.ProxyID
	if input.PreserveProxy && strings.TrimSpace(proxySource) == "" && proxyID == nil && existing != nil {
		proxySource = existing.ProxySource
		proxyID = existing.ProxyID
	}
	proxySource, err = normalizeReauthProxySource(proxySource, proxyID)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_PROXY_SOURCE_INVALID", "invalid re-login proxy source")
	}
	if proxySource == OpenAIOAuthReauthProxySourceManagedProxy {
		if _, err := s.reauthProxyURL(ctx, proxyID); err != nil {
			return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_PROXY_INVALID", "selected re-login proxy is unavailable")
		}
	}
	if proxySource == OpenAIOAuthReauthProxySourceMihomo && s.acquireMihomoProxy == nil {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_PROXY_INVALID", "selected re-login proxy is unavailable")
	}
	email, err := normalizeReauthEmail(input.LoginEmail)
	if err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(input.CredentialMode)
	if mode == "" {
		mode = OpenAIOAuthReauthModeEmailOTPURL
	}
	if mode != OpenAIOAuthReauthModeEmailOTPURL && mode != OpenAIOAuthReauthModePasswordTOTP {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_MODE_INVALID", "invalid OpenAI re-login credential mode")
	}
	stored := &OpenAIOAuthReauthStoredConfig{AccountID: accountID, LoginEmail: email, CredentialMode: mode, ProxySource: proxySource, ProxyID: proxyID, UpdatedAt: time.Now()}
	if existing != nil && existing.CredentialMode == mode {
		stored.PasswordCiphertext = existing.PasswordCiphertext
		stored.TOTPSecretCiphertext = existing.TOTPSecretCiphertext
		stored.OTPURLCiphertext = existing.OTPURLCiphertext
	}
	if input.ClearPassword {
		stored.PasswordCiphertext = ""
	}
	if input.ClearTOTP {
		stored.TOTPSecretCiphertext = ""
	}

	switch mode {
	case OpenAIOAuthReauthModeEmailOTPURL:
		stored.PasswordCiphertext = ""
		stored.TOTPSecretCiphertext = ""
		if raw := strings.TrimSpace(input.OTPURL); raw != "" {
			u, validateErr := ValidateOpenAIOAuthReauthURL(raw)
			if validateErr != nil {
				return nil, validateErr
			}
			stored.OTPURLCiphertext, err = s.encryptor.Encrypt(u.String())
			if err != nil {
				return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_ENCRYPT_FAILED", "failed to encrypt OTP API URL")
			}
		}
		if stored.OTPURLCiphertext == "" {
			return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_OTP_URL_REQUIRED", "an OTP mailbox URL is required")
		}
	case OpenAIOAuthReauthModePasswordTOTP:
		stored.OTPURLCiphertext = ""
		if raw := input.Password; raw != "" {
			stored.PasswordCiphertext, err = s.encryptor.Encrypt(raw)
			if err != nil {
				return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_ENCRYPT_FAILED", "failed to encrypt login password")
			}
		}
		if stored.PasswordCiphertext == "" {
			return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_PASSWORD_REQUIRED", "a login password is required")
		}
		if raw := strings.TrimSpace(input.TOTPSecret); raw != "" {
			stored.TOTPSecretCiphertext, err = s.encryptor.Encrypt(raw)
			if err != nil {
				return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_ENCRYPT_FAILED", "failed to encrypt TOTP secret")
			}
		}
	}
	if err := s.repo.UpsertConfig(ctx, stored); err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_SAVE_FAILED", "failed to save re-login configuration")
	}
	return s.configView(ctx, stored)
}

func (s *OpenAIOAuthReauthService) configView(_ context.Context, stored *OpenAIOAuthReauthStoredConfig) (*OpenAIOAuthReauthConfig, error) {
	if stored == nil {
		return nil, nil
	}
	mode := strings.TrimSpace(stored.CredentialMode)
	if mode == "" {
		mode = OpenAIOAuthReauthModeEmailOTPURL
	}
	proxySource, err := normalizeReauthProxySource(stored.ProxySource, stored.ProxyID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_PROXY_SOURCE_INVALID", "saved re-login proxy source is invalid")
	}
	view := &OpenAIOAuthReauthConfig{
		AccountID: stored.AccountID, LoginEmail: stored.LoginEmail, CredentialMode: mode,
		ProxySource: proxySource, ProxyID: stored.ProxyID, UpdatedAt: stored.UpdatedAt,
	}
	switch mode {
	case OpenAIOAuthReauthModeEmailOTPURL:
		plain, err := s.decryptReauthSecret(stored.OTPURLCiphertext, "saved OTP mailbox configuration cannot be decrypted")
		if err != nil {
			return nil, err
		}
		view.URLMasked = MaskOpenAIOAuthReauthURL(plain)
		view.Configured = plain != ""
	case OpenAIOAuthReauthModePasswordTOTP:
		password, err := s.decryptReauthSecret(stored.PasswordCiphertext, "saved login password cannot be decrypted")
		if err != nil {
			return nil, err
		}
		view.PasswordConfigured = password != ""
		if stored.TOTPSecretCiphertext != "" {
			totp, decryptErr := s.decryptReauthSecret(stored.TOTPSecretCiphertext, "saved TOTP secret cannot be decrypted")
			if decryptErr != nil {
				return nil, decryptErr
			}
			view.TOTPConfigured = totp != ""
		}
		view.Configured = view.PasswordConfigured
	default:
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_MODE_INVALID", "saved re-login credential mode is invalid")
	}
	return view, nil
}

func (s *OpenAIOAuthReauthService) decryptReauthSecret(ciphertext, message string) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return "", nil
	}
	plain, err := s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return "", infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_DECRYPT_FAILED", message)
	}
	return plain, nil
}

func (s *OpenAIOAuthReauthService) GetStatus(ctx context.Context, accountID int64) (*OpenAIOAuthReauthStatus, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if _, err := s.accountFor(ctx, accountID); err != nil {
		return nil, err
	}
	status := &OpenAIOAuthReauthStatus{}
	stored, err := s.repo.GetConfig(ctx, accountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_READ_FAILED", "failed to read OTP mailbox configuration")
	}
	if stored != nil {
		status.Config, err = s.configView(ctx, stored)
		if err != nil {
			return nil, err
		}
	}
	record, err := s.repo.GetLatestTask(ctx, accountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_TASK_READ_FAILED", "failed to read re-login task")
	}
	status.Task = safeReauthTask(record)
	return status, nil
}

func (s *OpenAIOAuthReauthService) CreateTask(ctx context.Context, accountID int64) (*OpenAIOAuthReauthTask, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if err := s.ensureDurableEncryption(); err != nil {
		return nil, err
	}
	account, err := s.accountFor(ctx, accountID)
	if err != nil {
		return nil, err
	}
	stored, err := s.repo.GetConfig(ctx, accountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CONFIG_READ_FAILED", "failed to read OTP mailbox configuration")
	}
	if stored == nil {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_CONFIG_REQUIRED", "save a re-login configuration first")
	}
	view, err := s.configView(ctx, stored)
	if err != nil {
		return nil, err
	}
	if view == nil || !view.Configured {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_CONFIG_REQUIRED", "save a complete re-login configuration first")
	}
	expectedCredentialsHash, err := hashReauthCredentials(account.Credentials)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_CREDENTIAL_SNAPSHOT_FAILED", "failed to snapshot account credentials")
	}
	record, err := s.repo.CreateTask(ctx, accountID, expectedCredentialsHash)
	if err != nil {
		if infraerrors.Code(err) == http.StatusConflict {
			return nil, err
		}
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_TASK_CREATE_FAILED", "failed to create re-login task")
	}
	return safeReauthTask(record), nil
}

// OpenAIOAuthReauthClaim is returned only to an authenticated worker. Secret
// fields are plaintext in this in-memory response and must never be logged.
type OpenAIOAuthReauthClaim struct {
	TaskID         int64  `json:"task_id"`
	AccountID      int64  `json:"account_id"`
	LoginEmail     string `json:"login_email"`
	CredentialMode string `json:"credential_mode"`
	Password       string `json:"password,omitempty"`
	TOTPSecret     string `json:"totp_secret,omitempty"`
	OTPURL         string `json:"otp_url,omitempty"`
	AuthURL        string `json:"auth_url,omitempty"`
	ProxyURL       string `json:"proxy_url,omitempty"`
}

func (s *OpenAIOAuthReauthService) ClaimTask(ctx context.Context, workerID string) (*OpenAIOAuthReauthClaim, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 128 {
		return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_WORKER_ID_INVALID", "worker id is required")
	}
	record, err := s.repo.ClaimNextTask(ctx, workerID, openAIOAuthReauthStaleAfter)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_TASK_CLAIM_FAILED", "failed to claim re-login task")
	}
	if record == nil {
		return nil, nil
	}
	// A stale task may be reclaimed by another worker. Release the previous
	// in-memory lease before resolving a fresh egress for the new attempt.
	s.releaseMihomoLease(record.ID)
	account, err := s.accountFor(ctx, record.AccountID)
	if err != nil {
		_ = s.repo.MarkFailed(ctx, record.ID, workerID, "account is no longer eligible for OpenAI OAuth re-login")
		return nil, nil
	}
	stored, err := s.repo.GetConfig(ctx, record.AccountID)
	if err != nil || stored == nil {
		_ = s.repo.MarkFailed(ctx, record.ID, workerID, "re-login configuration is missing")
		return nil, nil
	}
	mode := strings.TrimSpace(stored.CredentialMode)
	if mode == "" {
		mode = OpenAIOAuthReauthModeEmailOTPURL
	}
	proxySource, proxyErr := normalizeReauthProxySource(stored.ProxySource, stored.ProxyID)
	if proxyErr != nil {
		_ = s.repo.MarkFailed(ctx, record.ID, workerID, "configured re-login proxy source is invalid")
		return nil, nil
	}
	proxyID := stored.ProxyID
	proxyError := "configured re-login proxy is unavailable"
	var proxyURL string
	var release func()
	switch proxySource {
	case OpenAIOAuthReauthProxySourceAccount:
		proxyID = account.ProxyID
		proxyError = "configured account proxy is unavailable"
		proxyURL, proxyErr = s.reauthProxyURL(ctx, proxyID)
	case OpenAIOAuthReauthProxySourceManagedProxy:
		proxyURL, proxyErr = s.reauthProxyURL(ctx, proxyID)
	case OpenAIOAuthReauthProxySourceMihomo:
		proxyError = "managed Mihomo proxy is unavailable"
		if s.acquireMihomoProxy == nil {
			proxyErr = errors.New("managed Mihomo proxy is unavailable")
		} else {
			proxyURL, release, proxyErr = s.acquireMihomoProxy(ctx, fmt.Sprintf("openai-reauth-task:%d", record.ID))
		}
	}
	leaseStored := false
	if release != nil {
		defer func() {
			if !leaseStored {
				release()
			}
		}()
	}
	if proxyErr != nil {
		_ = s.repo.MarkFailed(ctx, record.ID, workerID, proxyError)
		return nil, nil
	}
	claim := &OpenAIOAuthReauthClaim{
		TaskID: record.ID, AccountID: record.AccountID, LoginEmail: stored.LoginEmail,
		CredentialMode: mode, ProxyURL: proxyURL,
	}
	switch mode {
	case OpenAIOAuthReauthModeEmailOTPURL:
		claim.OTPURL, err = s.decryptReauthSecret(stored.OTPURLCiphertext, "OTP mailbox configuration cannot be decrypted")
		if err != nil || claim.OTPURL == "" {
			_ = s.repo.MarkFailed(ctx, record.ID, workerID, "OTP mailbox configuration cannot be decrypted")
			return nil, nil
		}
		if _, err := ValidateOpenAIOAuthReauthURL(claim.OTPURL); err != nil {
			_ = s.repo.MarkFailed(ctx, record.ID, workerID, "OTP mailbox URL is invalid")
			return nil, nil
		}
		var auth *OpenAIAuthURLResult
		var authErr error
		if proxySource == OpenAIOAuthReauthProxySourceMihomo {
			auth, authErr = s.oauth.GenerateAuthURLWithProxyURL(ctx, proxyURL, "", PlatformOpenAI)
		} else {
			auth, authErr = s.oauth.GenerateAuthURL(ctx, proxyID, "", PlatformOpenAI)
		}
		if authErr != nil {
			_ = s.repo.MarkFailed(ctx, record.ID, workerID, "failed to create OpenAI authorization session")
			return nil, nil
		}
		if err := s.repo.SetSession(ctx, record.ID, workerID, auth.SessionID); err != nil {
			_ = s.repo.MarkFailed(ctx, record.ID, workerID, "failed to persist OpenAI authorization session")
			return nil, nil
		}
		claim.AuthURL = auth.AuthURL
	case OpenAIOAuthReauthModePasswordTOTP:
		claim.Password, err = s.decryptReauthSecret(stored.PasswordCiphertext, "login password cannot be decrypted")
		if err != nil || claim.Password == "" {
			_ = s.repo.MarkFailed(ctx, record.ID, workerID, "login password cannot be decrypted")
			return nil, nil
		}
		if stored.TOTPSecretCiphertext != "" {
			claim.TOTPSecret, err = s.decryptReauthSecret(stored.TOTPSecretCiphertext, "TOTP secret cannot be decrypted")
			if err != nil {
				_ = s.repo.MarkFailed(ctx, record.ID, workerID, "TOTP secret cannot be decrypted")
				return nil, nil
			}
		}
	default:
		_ = s.repo.MarkFailed(ctx, record.ID, workerID, "re-login credential mode is invalid")
		return nil, nil
	}
	if release != nil {
		s.replaceMihomoLease(record.ID, release)
		leaseStored = true
	}
	return claim, nil
}

func (s *OpenAIOAuthReauthService) UpdateStage(ctx context.Context, taskID int64, workerID, stage string) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if !validWorkerReauthStage(stage) {
		return infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_STAGE_INVALID", "invalid re-login task stage")
	}
	if err := s.repo.UpdateStage(ctx, taskID, strings.TrimSpace(workerID), stage); err != nil {
		return infraerrors.New(http.StatusConflict, "OPENAI_REAUTH_TASK_NOT_ACTIVE", "re-login task is no longer active")
	}
	return nil
}

func parseReauthCallback(callback string) (code, state string, err error) {
	if len(callback) == 0 || len(callback) > maxOpenAIOAuthReauthURL {
		return "", "", infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_CALLBACK_INVALID", "invalid OAuth callback")
	}
	u, parseErr := url.Parse(callback)
	if parseErr != nil || u == nil {
		return "", "", infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_CALLBACK_INVALID", "invalid OAuth callback")
	}
	if u.Scheme != "http" || !strings.EqualFold(u.Host, "localhost:1455") || u.User != nil ||
		u.Path != "/auth/callback" || u.RawPath != "" || u.Fragment != "" {
		return "", "", infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_CALLBACK_INVALID", "invalid OAuth callback")
	}
	q := u.Query()
	code = strings.TrimSpace(q.Get("code"))
	state = strings.TrimSpace(q.Get("state"))
	if code == "" || state == "" || len(code) > 2048 || len(state) > 512 {
		return "", "", infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_CALLBACK_INVALID", "OAuth callback is missing code or state")
	}
	return code, state, nil
}

func (s *OpenAIOAuthReauthService) SubmitCallback(ctx context.Context, taskID int64, workerID, callback string) (*OpenAIOAuthReauthTask, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	record, started, err := s.repo.BeginCallback(ctx, taskID, strings.TrimSpace(workerID))
	if err != nil {
		return nil, infraerrors.New(http.StatusConflict, "OPENAI_REAUTH_TASK_NOT_ACTIVE", "re-login task is no longer active")
	}
	if !started {
		return safeReauthTask(record), nil
	}
	defer s.releaseMihomoLease(taskID)
	account, err := s.accountFor(ctx, record.AccountID)
	if err != nil {
		return s.failCallback(ctx, taskID, "account is no longer eligible for OpenAI OAuth re-login", err)
	}
	currentCredentialsHash, err := hashReauthCredentials(account.Credentials)
	if err != nil || subtle.ConstantTimeCompare([]byte(currentCredentialsHash), []byte(record.ExpectedCredentialsHash)) != 1 {
		return s.failCallback(ctx, taskID, "account credentials changed while re-login was running", errors.New("credential snapshot mismatch"))
	}
	code, state, err := parseReauthCallback(callback)
	if err != nil {
		return s.failCallback(ctx, taskID, "invalid OAuth callback", err)
	}
	if err := s.repo.UpdateStage(ctx, taskID, record.WorkerID, OpenAIOAuthReauthStageExchangingToken); err != nil {
		return s.failCallback(ctx, taskID, "re-login task state changed unexpectedly", err)
	}
	tokenInfo, err := s.oauth.ExchangeCode(ctx, &OpenAIExchangeCodeInput{
		SessionID: record.AuthSessionID, Code: code, State: state,
	})
	if err != nil {
		return s.failCallback(ctx, taskID, "OpenAI authorization code exchange failed", err)
	}
	return s.applyReauthTokenInfo(ctx, record, account, tokenInfo, nil)
}

func (s *OpenAIOAuthReauthService) SubmitCredentials(ctx context.Context, taskID int64, workerID string, credentials, workerExtra map[string]any) (*OpenAIOAuthReauthTask, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	record, started, err := s.repo.BeginDirectCallback(ctx, taskID, strings.TrimSpace(workerID))
	if err != nil {
		return nil, infraerrors.New(http.StatusConflict, "OPENAI_REAUTH_TASK_NOT_ACTIVE", "re-login task is no longer active")
	}
	if !started {
		return safeReauthTask(record), nil
	}
	defer s.releaseMihomoLease(taskID)
	stored, configErr := s.repo.GetConfig(ctx, record.AccountID)
	if configErr != nil || stored == nil || strings.TrimSpace(stored.CredentialMode) != OpenAIOAuthReauthModePasswordTOTP {
		return s.failCallback(ctx, taskID, "re-login task does not accept direct credentials", errors.New("direct credentials require password/TOTP mode"))
	}
	account, err := s.accountFor(ctx, record.AccountID)
	if err != nil {
		return s.failCallback(ctx, taskID, "account is no longer eligible for OpenAI OAuth re-login", err)
	}
	currentCredentialsHash, err := hashReauthCredentials(account.Credentials)
	if err != nil || subtle.ConstantTimeCompare([]byte(currentCredentialsHash), []byte(record.ExpectedCredentialsHash)) != 1 {
		return s.failCallback(ctx, taskID, "account credentials changed while re-login was running", errors.New("credential snapshot mismatch"))
	}
	tokenInfo, extra, err := directReauthTokenInfo(credentials, workerExtra)
	if err != nil {
		return s.failCallback(ctx, taskID, "worker returned invalid OpenAI OAuth credentials", err)
	}
	return s.applyReauthTokenInfo(ctx, record, account, tokenInfo, extra)
}

func directReauthTokenInfo(credentials, workerExtra map[string]any) (*OpenAITokenInfo, map[string]any, error) {
	accessToken := reauthMapString(credentials, "access_token")
	refreshToken := reauthMapString(credentials, "refresh_token")
	idToken := reauthMapString(credentials, "id_token")
	if accessToken == "" || refreshToken == "" || idToken == "" {
		return nil, nil, errors.New("access_token, refresh_token and id_token are required")
	}
	claims, err := openai.ParseIDToken(idToken)
	if err != nil {
		return nil, nil, fmt.Errorf("parse id token: %w", err)
	}
	userInfo := claims.GetUserInfo()
	info := &OpenAITokenInfo{
		AccessToken: accessToken, RefreshToken: refreshToken, IDToken: idToken,
		ExpiresAt: claims.Exp, ClientID: firstNonEmptyReauth(
			reauthMapString(credentials, "client_id"), reauthMapString(workerExtra, "client_id"), openai.ClientID,
		),
	}
	if claims.Exp > 0 {
		info.ExpiresIn = claims.Exp - time.Now().Unix()
		if info.ExpiresIn < 0 {
			info.ExpiresIn = 0
		}
	}
	if userInfo != nil {
		info.Email = userInfo.Email
		info.ChatGPTAccountID = userInfo.ChatGPTAccountID
		info.ChatGPTUserID = userInfo.ChatGPTUserID
		info.OrganizationID = userInfo.OrganizationID
		info.PlanType = userInfo.PlanType
	}
	if value := reauthMapString(workerExtra, "privacy_mode"); value != "" {
		info.PrivacyMode = value
	}
	if value := reauthMapString(workerExtra, "subscription_expires_at"); value != "" {
		info.SubscriptionExpiresAt = value
	}
	extra := map[string]any{}
	for _, key := range []string{"email", "privacy_mode"} {
		if value := strings.TrimSpace(fmt.Sprint(workerExtra[key])); value != "" && value != "<nil>" {
			extra[key] = value
		}
	}
	return info, extra, nil
}

func reauthMapString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return strings.TrimSpace(value.String())
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func firstNonEmptyReauth(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (s *OpenAIOAuthReauthService) applyReauthTokenInfo(ctx context.Context, record *OpenAIOAuthReauthTaskRecord, account *Account, tokenInfo *OpenAITokenInfo, workerExtra map[string]any) (*OpenAIOAuthReauthTask, error) {
	taskID := record.ID
	if err := validateReauthToken(account, tokenInfo); err != nil {
		return s.failCallback(ctx, taskID, "new OAuth identity does not match the account", err)
	}
	if strings.TrimSpace(tokenInfo.RefreshToken) == "" || strings.TrimSpace(tokenInfo.AccessToken) == "" {
		return s.failCallback(ctx, taskID, "OpenAI did not return a usable OAuth token", errors.New("missing token"))
	}
	newCredentials := s.oauth.BuildAccountCredentials(tokenInfo)
	for key, value := range account.Credentials {
		if _, exists := newCredentials[key]; !exists {
			newCredentials[key] = value
		}
	}
	newCredentials["_token_version"] = time.Now().UnixMilli()
	newCredentials = SanitizeStoredCredentials(account.Platform, newCredentials)
	extra := map[string]any{}
	if tokenInfo.Email != "" {
		extra["email"] = tokenInfo.Email
	}
	if tokenInfo.PrivacyMode != "" {
		extra["privacy_mode"] = tokenInfo.PrivacyMode
	}
	for key, value := range workerExtra {
		if _, exists := extra[key]; !exists {
			extra[key] = value
		}
	}
	if err := s.repo.UpdateStage(ctx, taskID, record.WorkerID, OpenAIOAuthReauthStageApplyingCredentials); err != nil {
		return s.failCallback(ctx, taskID, "re-login task state changed unexpectedly", err)
	}
	taskFinalized, err := s.applyCredentials(ctx, taskID, record.WorkerID, account, account.Credentials, newCredentials, extra)
	if err != nil {
		return s.failCallback(ctx, taskID, "failed to apply new OAuth credentials", err)
	}
	if s.tokenCacheInvalidator != nil {
		updatedAccount := *account
		updatedAccount.Credentials = newCredentials
		if err := s.tokenCacheInvalidator.InvalidateToken(ctx, &updatedAccount); err != nil {
			// Credentials are already durable; cache invalidation is retried by the
			// normal token path and must not make a successful login look failed.
			slog.Warn("openai_oauth_reauth_token_cache_invalidation_failed", "account_id", account.ID, "task_id", taskID)
		}
	}
	if s.runtimeBlocker != nil {
		s.runtimeBlocker.ClearAccountSchedulingBlock(account.ID)
	}
	if !taskFinalized {
		if err := s.repo.MarkSucceeded(ctx, taskID, record.WorkerID); err != nil {
			return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_REAUTH_TASK_FINALIZE_FAILED", "credentials applied but task finalization failed")
		}
	}
	final, _ := s.repo.GetTask(ctx, taskID)
	return safeReauthTask(final), nil
}

func (s *OpenAIOAuthReauthService) failCallback(ctx context.Context, taskID int64, public string, cause error) (*OpenAIOAuthReauthTask, error) {
	record, _ := s.repo.GetTask(ctx, taskID)
	workerID := ""
	if record != nil {
		workerID = record.WorkerID
	}
	_ = s.repo.MarkFailed(ctx, taskID, workerID, public)
	if appErr, ok := cause.(*infraerrors.ApplicationError); ok {
		return nil, infraerrors.New(int(appErr.Code), "OPENAI_REAUTH_FAILED", public)
	}
	return nil, infraerrors.New(http.StatusBadRequest, "OPENAI_REAUTH_FAILED", public)
}

func (s *OpenAIOAuthReauthService) FailTask(ctx context.Context, taskID int64, workerID, reason string) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	reason = sanitizeReauthError(reason)
	if reason == "" {
		reason = "worker reported a re-login failure"
	}
	if err := s.repo.MarkFailed(ctx, taskID, strings.TrimSpace(workerID), reason); err != nil {
		return infraerrors.New(http.StatusConflict, "OPENAI_REAUTH_TASK_NOT_ACTIVE", "re-login task is no longer active")
	}
	s.releaseMihomoLease(taskID)
	return nil
}

func (s *OpenAIOAuthReauthService) applyCredentials(ctx context.Context, taskID int64, workerID string, account *Account, expectedCredentials, credentials, extra map[string]any) (bool, error) {
	changed, err := s.credentialUpdater.ApplyOpenAIOAuthReauth(ctx, taskID, workerID, account.ID, expectedCredentials, credentials, extra)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, infraerrors.Conflict("OPENAI_REAUTH_ACCOUNT_CHANGED", "account credentials changed while re-login was running")
	}
	return true, nil
}

func validateReauthToken(account *Account, tokenInfo *OpenAITokenInfo) error {
	if account == nil || tokenInfo == nil {
		return infraerrors.BadRequest("OPENAI_REAUTH_IDENTITY_MISMATCH", "OpenAI token identity is missing")
	}
	hasStoredIdentity := false
	oldAccountID := strings.TrimSpace(account.GetCredential("chatgpt_account_id"))
	newAccountID := strings.TrimSpace(tokenInfo.ChatGPTAccountID)
	if oldAccountID != "" {
		hasStoredIdentity = true
		if newAccountID == "" || subtle.ConstantTimeCompare([]byte(oldAccountID), []byte(newAccountID)) != 1 {
			return infraerrors.Conflict("OPENAI_REAUTH_IDENTITY_MISMATCH", "new OAuth login belongs to a different ChatGPT account")
		}
	}
	oldUserID := strings.TrimSpace(account.GetCredential("chatgpt_user_id"))
	newUserID := strings.TrimSpace(tokenInfo.ChatGPTUserID)
	if oldUserID != "" {
		hasStoredIdentity = true
		if newUserID == "" || subtle.ConstantTimeCompare([]byte(oldUserID), []byte(newUserID)) != 1 {
			return infraerrors.Conflict("OPENAI_REAUTH_IDENTITY_MISMATCH", "new OAuth login belongs to a different ChatGPT user")
		}
	}
	oldEmail := strings.ToLower(strings.TrimSpace(account.GetCredential("email")))
	newEmail := strings.ToLower(strings.TrimSpace(tokenInfo.Email))
	if oldEmail != "" {
		hasStoredIdentity = true
		if newEmail == "" || oldEmail != newEmail {
			return infraerrors.Conflict("OPENAI_REAUTH_EMAIL_MISMATCH", "new OAuth login email does not match the account")
		}
	}
	if !hasStoredIdentity {
		return infraerrors.BadRequest("OPENAI_REAUTH_IDENTITY_MISSING", "account has no stored OpenAI identity for verification")
	}
	return nil
}

func hashReauthCredentials(credentials map[string]any) (string, error) {
	payload, err := json.Marshal(credentials)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validWorkerReauthStage(stage string) bool {
	switch stage {
	case OpenAIOAuthReauthStageStarting, OpenAIOAuthReauthStageProtocolConnecting,
		OpenAIOAuthReauthStageEmailSubmitted, OpenAIOAuthReauthStageWaitingOTP,
		OpenAIOAuthReauthStageOTPSubmitted, OpenAIOAuthReauthStagePasswordSubmitted,
		OpenAIOAuthReauthStageMFASubmitted, OpenAIOAuthReauthStageWaitingCallback:
		return true
	default:
		return false
	}
}

func safeReauthTask(record *OpenAIOAuthReauthTaskRecord) *OpenAIOAuthReauthTask {
	if record == nil {
		return nil
	}
	return &OpenAIOAuthReauthTask{
		ID: record.ID, AccountID: record.AccountID, Status: record.Status,
		Stage: record.Stage, Error: sanitizeReauthError(record.Error), Attempt: record.Attempt,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, FinishedAt: record.FinishedAt,
	}
}

func sanitizeReauthError(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return ""
	}
	// Do not let callback URLs, bearer-like strings, or long provider payloads
	// become durable task logs.
	if len(msg) > maxOpenAIOAuthReauthError {
		msg = msg[:maxOpenAIOAuthReauthError]
	}
	for _, marker := range []string{"access_token", "refresh_token", "code=", "state=", "?key=", "&key="} {
		if strings.Contains(strings.ToLower(msg), marker) {
			return "re-login failed; sensitive provider details were omitted"
		}
	}
	return msg
}
