package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// 智能运维 → 凭证守护：账号令牌巡检 / 自动重登 / 错误态自愈。
//
// 巡检：用账号当前 access_token 调测活接口，区分「令牌失效 / 正常 / 临时异常」。
// 修复：令牌失效 → 用配置里的邮箱+密码+2FA 重新登录，写回新凭据并恢复调度；
// 探活正常但账号仍处于 error 态 → 清除错误态并恢复调度（避免禁用死锁）。
const accountTokenGuardSettingsKey = "account_token_guard_config_v1"

const (
	AccountTokenGuardProbeOK        = "ok"
	AccountTokenGuardProbeAuth      = "auth"
	AccountTokenGuardProbeTransient = "transient"

	AccountTokenGuardEventProbeOK     = "probe_ok"
	AccountTokenGuardEventProbeAuth   = "probe_auth"
	AccountTokenGuardEventProbeTemp   = "probe_transient"
	AccountTokenGuardEventReloginOK   = "relogin_ok"
	AccountTokenGuardEventReloginFail = "relogin_failed"
	AccountTokenGuardEventStateFixed  = "state_fixed"
	AccountTokenGuardEventStateFail   = "state_failed"
	AccountTokenGuardEventManual      = "manual_run"
)

const (
	AccountTokenGuardJobPending   = "pending"
	AccountTokenGuardJobRunning   = "running"
	AccountTokenGuardJobSucceeded = "succeeded"
	AccountTokenGuardJobFailed    = "failed"
	AccountTokenGuardJobCanceled  = "canceled"
)

var ErrAccountTokenGuardRunning = errors.New("上一轮巡检仍在进行")

// AccountTokenGuardReloginAccount 是重登所需凭据（邮箱 / 密码 / 2FA 密钥）。
type AccountTokenGuardReloginAccount struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	MFASecret string `json:"mfa_secret"`
}

// AccountTokenGuardConfig 是页面上的全部可配置项。
type AccountTokenGuardConfig struct {
	Enabled             bool                              `json:"enabled"`
	GroupIDs            []int64                           `json:"group_ids"`
	IntervalSeconds     int                               `json:"interval_seconds"`
	ProbeEndpoint       string                            `json:"probe_endpoint"`
	ProbeModel          string                            `json:"probe_model"`
	ProbeHeaders        map[string]string                 `json:"probe_headers"`
	ProbeTimeoutSeconds int                               `json:"probe_timeout_seconds"`
	ProbeConcurrency    int                               `json:"probe_concurrency"`
	MaxProbePerCycle    int                               `json:"max_probe_per_cycle"`
	AutoRelogin         bool                              `json:"auto_relogin"`
	ReloginEndpoint     string                            `json:"relogin_endpoint"`
	ReloginHeaders      map[string]string                 `json:"relogin_headers"`
	ReloginAccounts     []AccountTokenGuardReloginAccount `json:"relogin_accounts"`
	RestoreSchedulable  bool                              `json:"restore_schedulable"`
	FailStreakThreshold int                               `json:"fail_streak_threshold"`
	BarkKey             string                            `json:"bark_key"`
	NotifyOnFix         bool                              `json:"notify_on_fix"`
	NotifyOnFail        bool                              `json:"notify_on_fail"`
}

// AccountTokenGuardState 是一个账号最近一次巡检的展示状态。
type AccountTokenGuardState struct {
	AccountID     int64      `json:"account_id"`
	AccountName   string     `json:"account_name"`
	AccountStatus string     `json:"account_status"`
	Schedulable   bool       `json:"schedulable"`
	ProbeState    string     `json:"probe_state"`
	ProbeDetail   string     `json:"probe_detail"`
	LatencyMS     int        `json:"latency_ms"`
	FailStreak    int        `json:"fail_streak"`
	LastProbeAt   *time.Time `json:"last_probe_at"`
	LastFixAt     *time.Time `json:"last_fix_at"`
	LastFixAction string     `json:"last_fix_action"`
	LastFixResult string     `json:"last_fix_result"`
	NeedsRelogin  bool       `json:"needs_relogin"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AccountTokenGuardEvent 是一条巡检 / 修复日志。
type AccountTokenGuardEvent struct {
	ID          int64     `json:"id"`
	AccountID   int64     `json:"account_id"`
	AccountName string    `json:"account_name"`
	Kind        string    `json:"kind"`
	Detail      string    `json:"detail"`
	LatencyMS   int       `json:"latency_ms"`
	CreatedAt   time.Time `json:"created_at"`
}

// AccountTokenGuardStats 描述最近一轮巡检的结果。
type AccountTokenGuardStats struct {
	Probed            int   `json:"probed"`
	Healthy           int   `json:"healthy"`
	AuthFailed        int   `json:"auth_failed"`
	Transient         int   `json:"transient"`
	Repaired          int   `json:"repaired"`
	StateFixed        int   `json:"state_fixed"`
	Failed            int   `json:"failed"`
	PersistenceErrors int   `json:"persistence_errors"`
	DurationMS        int64 `json:"duration_ms"`
	StartedAt         int64 `json:"started_at"`
}

// AccountTokenGuardJob 是一次后台巡检任务的可查询快照。
// 手动巡检不绑定浏览器请求的生命周期，页面断开后仍可通过 status 查询结果。
type AccountTokenGuardJob struct {
	ID              string                 `json:"id"`
	Status          string                 `json:"status"`
	Manual          bool                   `json:"manual"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	FinishedAt      *time.Time             `json:"finished_at,omitempty"`
	Total           int                    `json:"total"`
	Completed       int                    `json:"completed"`
	Stats           AccountTokenGuardStats `json:"stats"`
	Error           string                 `json:"error,omitempty"`
	CancelRequested bool                   `json:"cancel_requested,omitempty"`
	cancel          context.CancelFunc
}

// AccountTokenGuardRuntime 是页头展示的运行信息。
type AccountTokenGuardRuntime struct {
	Running     bool                   `json:"running"`
	LastRun     *time.Time             `json:"last_run"`
	LastMessage string                 `json:"last_message"`
	Stats       AccountTokenGuardStats `json:"stats"`
	Job         *AccountTokenGuardJob  `json:"job,omitempty"`
}

// AccountTokenGuardStatus 是状态接口返回体。
type AccountTokenGuardStatus struct {
	Config   AccountTokenGuardConfig  `json:"config"`
	Accounts []AccountTokenGuardState `json:"accounts"`
	Events   []AccountTokenGuardEvent `json:"events"`
	Runtime  AccountTokenGuardRuntime `json:"runtime"`
}

// AccountTokenGuardRepository 持久化巡检状态与日志。
type AccountTokenGuardRepository interface {
	UpsertState(ctx context.Context, state AccountTokenGuardState) error
	DeleteStatesExcept(ctx context.Context, accountIDs []int64) error
	RecordEvent(ctx context.Context, event AccountTokenGuardEvent) error
	ListEvents(ctx context.Context, offset, limit int) ([]AccountTokenGuardEvent, error)
	ListStates(ctx context.Context) ([]AccountTokenGuardState, error)
	PruneEvents(ctx context.Context, before time.Time) error
}

// accountTokenGuardAccounts 是巡检需要的最小账号仓储能力。
type accountTokenGuardAccounts interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
	ListAllWithFilters(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
	ClearError(ctx context.Context, id int64) error
	SetSchedulable(ctx context.Context, id int64, schedulable bool) error
}

type accountTokenGuardOperationsAccounts interface {
	ListCredentialOperationsAccountIDs(ctx context.Context) ([]int64, error)
}

// AccountTokenGuardProbeResult 是单个账号的探活结果。
type AccountTokenGuardProbeResult struct {
	State      string
	Detail     string
	LatencyMS  int
	Diagnostic AccountTokenGuardDiagnostic
}

// AccountTokenGuardDiagnostic 是不会包含凭据、URL、请求头值或响应正文的探活诊断。
type AccountTokenGuardDiagnostic struct {
	Code        string     `json:"code,omitempty"`
	HTTPStatus  int        `json:"http_status,omitempty"`
	BytesRead   int64      `json:"bytes_read,omitempty"`
	HeadersAt   *time.Time `json:"headers_at,omitempty"`
	FirstByteAt *time.Time `json:"first_byte_at,omitempty"`
	ResultAt    *time.Time `json:"result_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
}

// AccountTokenGuardService 负责凭证巡检与修复。
type AccountTokenGuardService struct {
	settings    SettingRepository
	repo        AccountTokenGuardRepository
	accounts    accountTokenGuardAccounts
	admin       AdminService
	invalidator TokenCacheInvalidator
	httpClient  *http.Client

	configMu sync.Mutex // Serializes config reads, saves and login credential merges.
	config   atomic.Value

	runMu     sync.Mutex
	stateMu   sync.Mutex
	lifecycle sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	jobMu     sync.Mutex
	job       *AccountTokenGuardJob
	rootCtx   context.Context
	loginMu   sync.Mutex
	logins    map[string]*OpenAITwoFALoginJob

	lastRun     time.Time
	lastMessage string
	stats       AccountTokenGuardStats

	cycleRunning   atomic.Bool
	cycleStartedAt atomic.Int64
}

func NewAccountTokenGuardService(settings SettingRepository, repo AccountTokenGuardRepository, accounts accountTokenGuardAccounts,
	admin AdminService, invalidator TokenCacheInvalidator) *AccountTokenGuardService {
	svc := &AccountTokenGuardService{
		settings: settings, repo: repo, accounts: accounts, admin: admin, invalidator: invalidator,
		// Per-request contexts provide the probe/relogin budget. A shorter
		// client-wide timeout would incorrectly cap the configured 900 second
		// probe budget.
		httpClient: &http.Client{},
	}
	svc.config.Store(defaultAccountTokenGuardConfig())
	return svc
}

func defaultAccountTokenGuardConfig() AccountTokenGuardConfig {
	return AccountTokenGuardConfig{
		Enabled:         false,
		IntervalSeconds: 300,
		ProbeEndpoint:   "https://session.ameng2027.xyz/api/v1/relogin/probe",
		ProbeModel:      "gpt-6-astra",
		ProbeHeaders: map[string]string{
			"X-Session-Studio-Probe":  "1",
			"X-Session-Studio-Client": "{{uuid}}",
		},
		ProbeTimeoutSeconds: 240,
		ProbeConcurrency:    6,
		MaxProbePerCycle:    12,
		AutoRelogin:         true,
		ReloginEndpoint:     "https://session.ameng2027.xyz/api/v1/relogin",
		ReloginHeaders: map[string]string{
			"X-Session-Studio-Relogin": "1",
			"X-Session-Studio-Client":  "{{uuid}}",
		},
		RestoreSchedulable:  true,
		FailStreakThreshold: 1,
	}
}

// ValidateAccountTokenGuardConfig 校验配置范围与 URL 合法性。
func ValidateAccountTokenGuardConfig(c AccountTokenGuardConfig) error {
	if c.IntervalSeconds < 30 || c.IntervalSeconds > 86400 {
		return errors.New("巡检间隔需要在 30 到 86400 秒之间")
	}
	if c.ProbeTimeoutSeconds < 5 || c.ProbeTimeoutSeconds > 900 {
		return errors.New("探活超时需要在 5 到 900 秒之间")
	}
	if c.ProbeConcurrency < 1 || c.ProbeConcurrency > 16 {
		return errors.New("并发探活数需要在 1 到 16 之间")
	}
	if c.MaxProbePerCycle < 1 || c.MaxProbePerCycle > 100 {
		return errors.New("每轮最多探测账号数需要在 1 到 100 之间")
	}
	if c.FailStreakThreshold < 1 || c.FailStreakThreshold > 10 {
		return errors.New("连续失效阈值需要在 1 到 10 之间")
	}
	if c.Enabled {
		if err := validateGuardHTTPURL(c.ProbeEndpoint, "probe_endpoint"); err != nil {
			return err
		}
		if c.ProbeModel == "" {
			return errors.New("启用守护时必须填写探活模型")
		}
		if c.AutoRelogin {
			if err := validateGuardHTTPURL(c.ReloginEndpoint, "relogin_endpoint"); err != nil {
				return err
			}
			if len(c.ReloginAccounts) == 0 {
				return errors.New("开启自动重登时必须至少配置一个重登账号")
			}
		}
	} else if c.ProbeEndpoint != "" {
		if err := validateGuardHTTPURL(c.ProbeEndpoint, "probe_endpoint"); err != nil {
			return err
		}
	}
	if err := validateGuardHeaders(c.ProbeHeaders, "probe_headers"); err != nil {
		return err
	}
	if err := validateGuardHeaders(c.ReloginHeaders, "relogin_headers"); err != nil {
		return err
	}
	for _, account := range c.ReloginAccounts {
		if strings.TrimSpace(account.Email) == "" || !strings.Contains(account.Email, "@") {
			return errors.New("重登账号邮箱不合法: " + account.Email)
		}
		if strings.TrimSpace(account.Password) == "" {
			return errors.New("重登账号缺少密码: " + account.Email)
		}
	}
	return nil
}

func validateGuardHTTPURL(raw, field string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New(field + " 不能为空")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New(field + " 需要是合法的 http/https 地址")
	}
	return nil
}

func normalizeAccountTokenGuardConfig(c AccountTokenGuardConfig) AccountTokenGuardConfig {
	c.ProbeEndpoint = strings.TrimRight(strings.TrimSpace(c.ProbeEndpoint), "/")
	c.ReloginEndpoint = strings.TrimRight(strings.TrimSpace(c.ReloginEndpoint), "/")
	c.ProbeModel = strings.TrimSpace(c.ProbeModel)
	c.BarkKey = strings.TrimSpace(c.BarkKey)
	c.ProbeHeaders = normalizeGuardHeaders(c.ProbeHeaders)
	c.ReloginHeaders = normalizeGuardHeaders(c.ReloginHeaders)
	if c.ProbeConcurrency <= 0 {
		c.ProbeConcurrency = 6
	}
	if c.MaxProbePerCycle <= 0 {
		c.MaxProbePerCycle = 12
	}
	if c.ProbeTimeoutSeconds <= 0 {
		c.ProbeTimeoutSeconds = 240
	}
	if c.IntervalSeconds <= 0 {
		c.IntervalSeconds = 300
	}
	if c.FailStreakThreshold <= 0 {
		c.FailStreakThreshold = 1
	}
	seenGroups := map[int64]bool{}
	groups := make([]int64, 0, len(c.GroupIDs))
	for _, id := range c.GroupIDs {
		if id <= 0 || seenGroups[id] {
			continue
		}
		seenGroups[id] = true
		groups = append(groups, id)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	c.GroupIDs = groups
	accounts := make([]AccountTokenGuardReloginAccount, 0, len(c.ReloginAccounts))
	seenMail := map[string]bool{}
	for _, account := range c.ReloginAccounts {
		account.Email = strings.ToLower(strings.TrimSpace(account.Email))
		account.Password = strings.TrimSpace(account.Password)
		account.MFASecret = strings.TrimSpace(account.MFASecret)
		if account.Email == "" || seenMail[account.Email] {
			continue
		}
		seenMail[account.Email] = true
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Email < accounts[j].Email })
	c.ReloginAccounts = accounts
	return c
}

func (s *AccountTokenGuardService) GetConfig(ctx context.Context) (AccountTokenGuardConfig, error) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	return s.getConfigLocked(ctx)
}

func (s *AccountTokenGuardService) getConfigLocked(ctx context.Context) (AccountTokenGuardConfig, error) {
	cfg := defaultAccountTokenGuardConfig()
	raw, err := s.settings.GetValue(ctx, accountTokenGuardSettingsKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return cfg, err
	}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return cfg, err
		}
	}
	cfg = normalizeAccountTokenGuardConfig(cfg)
	if err := ValidateAccountTokenGuardConfig(cfg); err != nil {
		return cfg, err
	}
	s.config.Store(cfg)
	return cfg, nil
}

func (s *AccountTokenGuardService) SaveConfig(ctx context.Context, cfg AccountTokenGuardConfig) (AccountTokenGuardConfig, error) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	return s.saveConfigLocked(ctx, cfg)
}

func (s *AccountTokenGuardService) saveConfigLocked(ctx context.Context, cfg AccountTokenGuardConfig) (AccountTokenGuardConfig, error) {
	cfg = normalizeAccountTokenGuardConfig(cfg)
	if err := ValidateAccountTokenGuardConfig(cfg); err != nil {
		return cfg, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	if err := s.settings.Set(ctx, accountTokenGuardSettingsKey, string(raw)); err != nil {
		return cfg, err
	}
	s.config.Store(cfg)
	return cfg, nil
}

func (s *AccountTokenGuardService) currentConfig() AccountTokenGuardConfig {
	if value, ok := s.config.Load().(AccountTokenGuardConfig); ok {
		return value
	}
	return defaultAccountTokenGuardConfig()
}

// StartRun 接受一轮后台巡检并立即返回。任务上下文独立于 HTTP 请求，避免浏览器断开
// 或反向代理 30 秒超时把仍在执行的巡检误报为失败。
func (s *AccountTokenGuardService) StartRun(manual bool) (*AccountTokenGuardJob, error) {
	s.lifecycle.Lock()
	if s.rootCtx != nil && s.cancel == nil {
		s.lifecycle.Unlock()
		return nil, errors.New("凭证守护服务已停止")
	}
	parent := s.rootCtx
	s.jobMu.Lock()
	defer func() {
		s.jobMu.Unlock()
		s.lifecycle.Unlock()
	}()
	if s.job != nil && (s.job.Status == AccountTokenGuardJobPending || s.job.Status == AccountTokenGuardJobRunning) {
		snapshot := *s.job
		snapshot.cancel = nil
		return &snapshot, ErrAccountTokenGuardRunning
	}
	if parent == nil {
		parent = context.Background()
	}
	jobCtx, cancel := context.WithCancel(parent)
	job := &AccountTokenGuardJob{ID: guardRandomID(), Status: AccountTokenGuardJobPending, Manual: manual, cancel: cancel}
	s.job = job
	s.wg.Add(1)
	go s.runJob(jobCtx, job)
	snapshot := *job
	snapshot.cancel = nil
	return &snapshot, nil
}

func (s *AccountTokenGuardService) runJob(ctx context.Context, job *AccountTokenGuardJob) {
	defer s.wg.Done()
	started := time.Now()
	s.jobMu.Lock()
	job.Status = AccountTokenGuardJobRunning
	job.StartedAt = &started
	s.jobMu.Unlock()
	stats, err := s.runCycle(ctx, job.Manual, job.ID)
	finished := time.Now()
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	job.FinishedAt = &finished
	job.Stats = stats
	job.cancel = nil
	switch {
	case errors.Is(err, context.Canceled):
		job.Status = AccountTokenGuardJobCanceled
		job.CancelRequested = true
	case err != nil:
		job.Status = AccountTokenGuardJobFailed
		job.Error = sanitizeGuardError(err)
	default:
		job.Status = AccountTokenGuardJobSucceeded
	}
}

// CancelRun 请求取消当前任务。探活请求会被取消，已开始的有界状态落库仍可完成。
func (s *AccountTokenGuardService) CancelRun(jobID string) (*AccountTokenGuardJob, error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.job == nil || s.job.ID != jobID {
		return nil, errors.New("巡检任务不存在")
	}
	if s.job.Status != AccountTokenGuardJobPending && s.job.Status != AccountTokenGuardJobRunning {
		snapshot := *s.job
		snapshot.cancel = nil
		return &snapshot, nil
	}
	s.job.CancelRequested = true
	if s.job.cancel != nil {
		s.job.cancel()
	}
	snapshot := *s.job
	snapshot.cancel = nil
	return &snapshot, nil
}

func (s *AccountTokenGuardService) Job(jobID string) (*AccountTokenGuardJob, bool) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.job == nil || (jobID != "" && s.job.ID != jobID) {
		return nil, false
	}
	snapshot := *s.job
	snapshot.cancel = nil
	return &snapshot, true
}

// Start 启动后台巡检循环；重复调用无副作用。
func (s *AccountTokenGuardService) Start() {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.rootCtx = ctx
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if _, err := s.GetConfig(ctx); err != nil {
			slog.Warn("account_token_guard_config_load_failed", "error", err)
		}
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if s.currentConfig().Enabled {
				if _, err := s.StartRun(false); err != nil && !errors.Is(err, ErrAccountTokenGuardRunning) {
					slog.Debug("account_token_guard_cycle_skipped", "reason", err.Error())
				}
			}
			interval := time.Duration(s.currentConfig().IntervalSeconds) * time.Second
			if interval < 30*time.Second {
				interval = 5 * time.Minute
			}
			timer.Reset(interval)
		}
	}()
}

func (s *AccountTokenGuardService) Stop() {
	s.clearTwoFALogins()
	s.lifecycle.Lock()
	if s.cancel == nil {
		s.lifecycle.Unlock()
		return
	}
	s.cancel()
	s.cancel = nil
	s.lifecycle.Unlock()
	s.jobMu.Lock()
	if s.job != nil && (s.job.Status == AccountTokenGuardJobPending || s.job.Status == AccountTokenGuardJobRunning) {
		s.job.CancelRequested = true
		if s.job.cancel != nil {
			s.job.cancel()
		}
	}
	s.jobMu.Unlock()
	s.wg.Wait()
}

// Status 返回页面需要的配置、账号状态、最近日志与运行信息。
func (s *AccountTokenGuardService) Status(ctx context.Context) (AccountTokenGuardStatus, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return AccountTokenGuardStatus{}, err
	}
	states, err := s.repo.ListStates(ctx)
	if err != nil {
		return AccountTokenGuardStatus{}, err
	}
	managed, err := s.credentialOperationsAccounts(ctx)
	if err != nil {
		return AccountTokenGuardStatus{}, err
	}
	visible := states[:0]
	for _, state := range states {
		if !managed[state.AccountID] {
			visible = append(visible, state)
		}
	}
	states = visible
	events, err := s.repo.ListEvents(ctx, 0, 100)
	if err != nil {
		return AccountTokenGuardStatus{}, err
	}
	for index := range states {
		states[index].NeedsRelogin = states[index].ProbeState == AccountTokenGuardProbeAuth
	}
	return AccountTokenGuardStatus{Config: cfg, Accounts: states, Events: events, Runtime: s.runtimeInfo()}, nil
}

func (s *AccountTokenGuardService) runtimeInfo() AccountTokenGuardRuntime {
	s.stateMu.Lock()
	info := AccountTokenGuardRuntime{LastMessage: s.lastMessage, Stats: s.stats}
	s.stateMu.Unlock()
	info.Running = s.cycleRunning.Load()
	if startedAt := s.cycleStartedAt.Load(); startedAt > 0 {
		started := time.Unix(startedAt, 0)
		info.LastRun = &started
	} else {
		s.stateMu.Lock()
		if !s.lastRun.IsZero() {
			last := s.lastRun
			info.LastRun = &last
		}
		s.stateMu.Unlock()
	}
	s.jobMu.Lock()
	if s.job != nil {
		snapshot := *s.job
		snapshot.cancel = nil
		info.Job = &snapshot
	}
	s.jobMu.Unlock()
	return info
}

// RunCycle 执行一轮巡检；manual 仅用于日志措辞。
func (s *AccountTokenGuardService) RunCycle(ctx context.Context, manual bool) (AccountTokenGuardStats, error) {
	return s.runCycle(ctx, manual, "")
}

func (s *AccountTokenGuardService) runCycle(ctx context.Context, manual bool, jobID string) (AccountTokenGuardStats, error) {
	if !s.runMu.TryLock() {
		return s.currentStats(), ErrAccountTokenGuardRunning
	}
	defer s.runMu.Unlock()
	started := time.Now()
	s.cycleRunning.Store(true)
	s.cycleStartedAt.Store(started.Unix())
	defer func() {
		s.cycleRunning.Store(false)
		s.cycleStartedAt.Store(0)
	}()
	cfg := s.currentConfig()
	waveSize := cfg.ProbeConcurrency
	if waveSize < 1 {
		waveSize = 1
	}
	probeCount := cfg.MaxProbePerCycle
	if probeCount < 1 {
		probeCount = waveSize
	}
	waves := (probeCount + waveSize - 1) / waveSize
	cycleLimit := time.Duration(waves)*time.Duration(cfg.ProbeTimeoutSeconds)*time.Second + time.Minute
	if cycleLimit < time.Duration(cfg.ProbeTimeoutSeconds+60)*time.Second {
		cycleLimit = time.Duration(cfg.ProbeTimeoutSeconds+60) * time.Second
	}
	if cycleLimit > 20*time.Minute {
		cycleLimit = 20 * time.Minute
	}
	runParent := ctx
	// The legacy synchronous endpoint may still be called by an HTTP client. Do
	// not let that request's cancellation interrupt a cycle that the server has
	// already accepted. New jobs use their own cancellable context.
	if jobID == "" {
		runParent = context.Background()
	}
	cycleCtx, cancelCycle := context.WithTimeout(runParent, cycleLimit)
	defer cancelCycle()
	accounts, err := s.listAccounts(cycleCtx, cfg)
	if err != nil {
		s.finishCycle(started, AccountTokenGuardStats{}, "巡检失败："+err.Error())
		return s.currentStats(), err
	}
	s.updateJobTotal(jobID, len(accounts))
	// Snapshot before progress upserts: those must not erase the previous
	// cycle's failure streak before the recovery threshold is evaluated.
	existing := s.statesMap(cycleCtx)
	results := make([]AccountTokenGuardProbeResult, len(accounts))
	semaphore := make(chan struct{}, cfg.ProbeConcurrency)
	var wg sync.WaitGroup
	for index := range accounts {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(position int) {
			defer wg.Done()
			defer func() { <-semaphore }()
			result := s.probe(cycleCtx, cfg, &accounts[position])
			result.Detail = withGuardJobID(result.Detail, jobID)
			results[position] = result
			// 探活完成即刻落库，页面无需等整轮结束即可看到进度。
			now := time.Now()
			storeCtx, storeCancel := newGuardStoreContext()
			// Do not replay historical fix fields: the repository preserves the
			// latest manual repair when progress has no LastFixAt of its own.
			state := AccountTokenGuardState{FailStreak: existing[accounts[position].ID].FailStreak}
			state.AccountID = accounts[position].ID
			state.AccountName = accounts[position].Name
			state.AccountStatus = accounts[position].Status
			state.Schedulable = accounts[position].Schedulable
			state.ProbeState, state.ProbeDetail, state.LatencyMS = result.State, result.Detail, result.LatencyMS
			state.LastProbeAt, state.UpdatedAt = &now, now
			_ = s.repo.UpsertState(storeCtx, state)
			storeCancel()
			s.updateJobProgress(jobID)
		}(index)
	}
	wg.Wait()
	if err := cycleCtx.Err(); err != nil {
		return s.currentStats(), err
	}

	stats := AccountTokenGuardStats{Probed: len(accounts), StartedAt: started.Unix()}
	states := make([]AccountTokenGuardState, 0, len(accounts))
	existing = s.statesMap(cycleCtx)
	for index := range accounts {
		if runParent.Err() != nil {
			break
		}
		account := &accounts[index]
		result := results[index]
		state := AccountTokenGuardState{AccountID: account.ID, AccountName: account.Name, AccountStatus: account.Status, Schedulable: account.Schedulable}
		if previous, ok := existing[account.ID]; ok {
			state = previous
		}
		state.ProbeState = result.State
		state.ProbeDetail = result.Detail
		state.LatencyMS = result.LatencyMS
		now := time.Now()
		state.LastProbeAt = &now
		state.UpdatedAt = now
		state.AccountName = account.Name
		state.AccountStatus = account.Status
		state.Schedulable = account.Schedulable

		switch result.State {
		case AccountTokenGuardProbeOK:
			stats.Healthy++
			state.FailStreak = 0
			if account.Status == StatusError {
				fixCtx, cancelFix := context.WithTimeout(runParent, time.Minute)
				recovered := s.recoverState(fixCtx, account, cfg)
				cancelFix()
				if recovered {
					stats.StateFixed++
					state.LastFixAt = &now
					state.LastFixAction = "状态自愈"
					state.LastFixResult = "清除错误态并恢复调度"
					markGuardStateRecovered(&state, cfg)
					s.recordEvent(cycleCtx, account, AccountTokenGuardEventStateFixed, "探活正常但账号处于 error 态，已清除错误并恢复调度", result.LatencyMS)
					s.notify(cfg, "凭证守护：已恢复账号调度", fmt.Sprintf("%s(#%d) 令牌有效但被禁用，已自动恢复调度", account.Name, account.ID), cfg.NotifyOnFix)
				} else {
					stats.Failed++
					state.LastFixAt = &now
					state.LastFixAction = "状态自愈"
					state.LastFixResult = "恢复失败"
					s.recordEvent(cycleCtx, account, AccountTokenGuardEventStateFail, "探活正常但账号 error 态恢复失败", result.LatencyMS)
					s.notify(cfg, "凭证守护：状态恢复失败", fmt.Sprintf("%s(#%d) 错误态恢复失败，请检查日志", account.Name, account.ID), cfg.NotifyOnFail)
				}
			}
		case AccountTokenGuardProbeAuth:
			stats.AuthFailed++
			state.FailStreak++
			s.recordEvent(cycleCtx, account, AccountTokenGuardEventProbeAuth, result.Detail, result.LatencyMS)
			if state.FailStreak >= cfg.FailStreakThreshold && cfg.AutoRelogin {
				// Login may take much longer than probing. Give each repair its
				// own budget, while retaining job cancellation and service Stop.
				fixCtx, cancelFix := context.WithTimeout(runParent, 25*time.Minute)
				action, fixErr := s.reloginAccount(fixCtx, cfg, account)
				cancelFix()
				if fixErr != nil {
					stats.Failed++
					state.LastFixAt = &now
					state.LastFixAction = "自动重登"
					state.LastFixResult = "失败: " + fixErr.Error()
					s.recordEvent(cycleCtx, account, AccountTokenGuardEventReloginFail, fixErr.Error(), 0)
					s.notify(cfg, "凭证守护：自动重登失败", fmt.Sprintf("%s(#%d) 重登失败：%s", account.Name, account.ID, truncateGuardText(fixErr.Error(), 120)), cfg.NotifyOnFail)
				} else {
					stats.Repaired++
					state.FailStreak = 0
					state.LastFixAt = &now
					state.LastFixAction = "自动重登"
					state.LastFixResult = action
					state.ProbeState = AccountTokenGuardProbeOK
					state.ProbeDetail = `{"code":"relogin_ok"}`
					markGuardStateRecovered(&state, cfg)
					s.recordEvent(cycleCtx, account, AccountTokenGuardEventReloginOK, action, 0)
					s.notify(cfg, "凭证守护：已自动重登", fmt.Sprintf("%s(#%d) 已重登并恢复调度", account.Name, account.ID), cfg.NotifyOnFix)
				}
			}
		default:
			stats.Transient++
			s.recordEvent(cycleCtx, account, AccountTokenGuardEventProbeTemp, result.Detail, result.LatencyMS)
		}
		states = append(states, state)
		storeCtx, storeCancel := newGuardStoreContext()
		if err := s.repo.UpsertState(storeCtx, state); err != nil {
			slog.Warn("account_token_guard_state_upsert_failed", "account_id", account.ID, "error", err)
		}
		storeCancel()
	}
	stats.DurationMS = time.Since(started).Milliseconds()
	message := fmt.Sprintf("巡检 %d 个账号：正常 %d，令牌失效 %d（已重登 %d），状态自愈 %d，临时异常 %d，失败 %d，耗时 %.1fs",
		stats.Probed, stats.Healthy, stats.AuthFailed, stats.Repaired, stats.StateFixed, stats.Transient, stats.Failed, float64(stats.DurationMS)/1000)
	if manual {
		s.recordEvent(cycleCtx, nil, AccountTokenGuardEventManual, message, 0)
	}
	stats.PersistenceErrors = s.persistStates(states)
	stats.Failed += stats.PersistenceErrors
	if stats.PersistenceErrors > 0 {
		message += fmt.Sprintf("，持久化失败 %d", stats.PersistenceErrors)
	}
	s.finishCycle(started, stats, message)
	if err := runParent.Err(); err != nil {
		return stats, err
	}
	slog.Info("account_token_guard_cycle_done", "probed", stats.Probed, "healthy", stats.Healthy,
		"auth_failed", stats.AuthFailed, "repaired", stats.Repaired, "state_fixed", stats.StateFixed)
	return stats, nil
}

func (s *AccountTokenGuardService) currentStats() AccountTokenGuardStats {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.stats
}

func (s *AccountTokenGuardService) updateJobTotal(jobID string, total int) {
	if jobID == "" {
		return
	}
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.job != nil && s.job.ID == jobID {
		s.job.Total = total
	}
}

func (s *AccountTokenGuardService) updateJobProgress(jobID string) {
	if jobID == "" {
		return
	}
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.job != nil && s.job.ID == jobID {
		s.job.Completed++
	}
}

func (s *AccountTokenGuardService) finishCycle(started time.Time, stats AccountTokenGuardStats, message string) {
	s.stateMu.Lock()
	s.lastRun = started
	s.stats = stats
	s.lastMessage = message
	s.stateMu.Unlock()
}

func (s *AccountTokenGuardService) listAccounts(ctx context.Context, cfg AccountTokenGuardConfig) ([]Account, error) {
	managed, err := s.credentialOperationsAccounts(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	out := make([]Account, 0, 32)
	appendAccount := func(account Account) {
		if seen[account.ID] || managed[account.ID] || !account.IsOAuth() || account.Platform != PlatformOpenAI || account.IsShadow() ||
			(account.Status != StatusActive && account.Status != StatusError) {
			return
		}
		seen[account.ID] = true
		out = append(out, account)
	}
	groupIDs := cfg.GroupIDs
	if len(groupIDs) == 0 {
		groupIDs = []int64{0}
	}
	for _, groupID := range groupIDs {
		// Scheduling queries only return active accounts. The admin query
		// includes errors; filter disabled accounts above. Leave status empty
		// because its "active" filter also excludes paused/cooled accounts.
		items, err := s.accounts.ListAllWithFilters(ctx, PlatformOpenAI, "", "", "", groupID, "")
		if err != nil {
			return nil, err
		}
		for _, account := range items {
			appendAccount(account)
		}
	}
	states := s.statesMap(ctx)
	sort.Slice(out, func(i, j int) bool {
		left, right := states[out[i].ID], states[out[j].ID]
		if left.LastProbeAt == nil && right.LastProbeAt != nil {
			return true
		}
		if left.LastProbeAt != nil && right.LastProbeAt == nil {
			return false
		}
		if left.LastProbeAt != nil && right.LastProbeAt != nil && !left.LastProbeAt.Equal(*right.LastProbeAt) {
			return left.LastProbeAt.Before(*right.LastProbeAt)
		}
		return out[i].ID < out[j].ID
	})
	total := len(out)
	if cfg.MaxProbePerCycle > 0 && total > cfg.MaxProbePerCycle {
		out = out[:cfg.MaxProbePerCycle]
	}
	slog.Info("account_token_guard_cycle_scope", "total", total, "probe_this_cycle", len(out))
	return out, nil
}

func (s *AccountTokenGuardService) statesMap(ctx context.Context) map[int64]AccountTokenGuardState {
	result := map[int64]AccountTokenGuardState{}
	states, err := s.repo.ListStates(ctx)
	if err != nil {
		return result
	}
	for _, state := range states {
		result[state.AccountID] = state
	}
	return result
}

func (s *AccountTokenGuardService) loadState(ctx context.Context, account *Account) AccountTokenGuardState {
	state := AccountTokenGuardState{AccountID: account.ID, AccountName: account.Name, AccountStatus: account.Status, Schedulable: account.Schedulable}
	states, err := s.repo.ListStates(ctx)
	if err != nil {
		return state
	}
	for _, item := range states {
		if item.AccountID == account.ID {
			item.AccountName = account.Name
			item.AccountStatus = account.Status
			item.Schedulable = account.Schedulable
			return item
		}
	}
	return state
}

func (s *AccountTokenGuardService) persistStates(states []AccountTokenGuardState) int {
	failures := 0
	for _, state := range states {
		storeCtx, storeCancel := newGuardStoreContext()
		if err := s.repo.UpsertState(storeCtx, state); err != nil {
			slog.Warn("account_token_guard_state_upsert_failed", "account_id", state.AccountID, "error", err)
			failures++
		}
		storeCancel()
	}
	// Do not delete states outside this cycle. MaxProbePerCycle deliberately
	// makes a cycle partial, so pruning here would erase the historical result
	// of every account that was not selected in this wave.
	storeCtx, storeCancel := newGuardStoreContext()
	if err := s.repo.PruneEvents(storeCtx, time.Now().Add(-14*24*time.Hour)); err != nil {
		slog.Warn("account_token_guard_event_prune_failed", "error", err)
		failures++
	}
	storeCancel()
	return failures
}

func (s *AccountTokenGuardService) recordEvent(ctx context.Context, account *Account, kind, detail string, latencyMS int) {
	event := AccountTokenGuardEvent{Kind: kind, Detail: truncateGuardText(detail, 1000), LatencyMS: latencyMS}
	if account != nil {
		event.AccountID = account.ID
		event.AccountName = account.Name
	}
	storeCtx, storeCancel := newGuardStoreContext()
	defer storeCancel()
	if err := s.repo.RecordEvent(storeCtx, event); err != nil {
		slog.Warn("account_token_guard_event_record_failed", "kind", kind, "error", err)
	}
}

func newGuardStoreContext() (context.Context, context.CancelFunc) {
	// Persistence is deliberately independent of the browser request and has
	// its own short upper bound. A canceled probe must not leave a half-written
	// state without making shutdown wait forever.
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// ReloginAccount 供页面手动触发单个账号重登。
func (s *AccountTokenGuardService) ReloginAccount(ctx context.Context, accountID int64) (string, error) {
	managed, err := s.credentialOperationsAccounts(ctx)
	if err != nil {
		return "", err
	}
	if managed[accountID] {
		return "", errors.New("账号已加入凭证运营，请在凭证运营中重登")
	}
	cfg := s.currentConfig()
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return "", errors.New("账号不存在")
	}
	action, fixErr := s.reloginAccount(ctx, cfg, account)
	now := time.Now()
	state := s.loadState(ctx, account)
	state.AccountID = account.ID
	state.AccountName = account.Name
	state.AccountStatus = account.Status
	state.Schedulable = account.Schedulable
	state.LastFixAt = &now
	state.LastFixAction = "手动重登"
	if fixErr != nil {
		state.LastFixResult = "失败: " + fixErr.Error()
		s.recordEvent(ctx, account, AccountTokenGuardEventReloginFail, "手动重登失败: "+fixErr.Error(), 0)
		_ = s.repo.UpsertState(ctx, state)
		return "", fixErr
	}
	state.FailStreak = 0
	state.ProbeState = AccountTokenGuardProbeOK
	state.ProbeDetail = "手动重登成功"
	state.LastFixResult = action
	markGuardStateRecovered(&state, cfg)
	_ = s.repo.UpsertState(ctx, state)
	s.recordEvent(ctx, account, AccountTokenGuardEventReloginOK, "手动重登: "+action, 0)
	return action, nil
}

func (s *AccountTokenGuardService) credentialOperationsAccounts(ctx context.Context) (map[int64]bool, error) {
	managed := make(map[int64]bool)
	reader, ok := s.accounts.(accountTokenGuardOperationsAccounts)
	if !ok {
		return managed, nil
	}
	ids, err := reader.ListCredentialOperationsAccountIDs(ctx)
	if err != nil {
		return nil, errors.New("无法读取凭证运营账号范围，已暂停旧守护操作")
	}
	for _, id := range ids {
		managed[id] = true
	}
	return managed, nil
}

func markGuardStateRecovered(state *AccountTokenGuardState, cfg AccountTokenGuardConfig) {
	state.AccountStatus = StatusActive
	if cfg.RestoreSchedulable {
		state.Schedulable = true
	}
}

func (s *AccountTokenGuardService) recoverState(ctx context.Context, account *Account, cfg AccountTokenGuardConfig) bool {
	if _, err := s.admin.ClearAccountError(ctx, account.ID); err != nil {
		slog.Warn("account_token_guard_clear_error_failed", "account_id", account.ID, "error", err)
		return false
	}
	if cfg.RestoreSchedulable {
		if _, err := s.admin.SetAccountSchedulable(ctx, account.ID, true); err != nil {
			slog.Warn("account_token_guard_set_schedulable_failed", "account_id", account.ID, "error", err)
			return false
		}
	}
	if s.invalidator != nil {
		if err := s.invalidator.InvalidateToken(ctx, account); err != nil {
			slog.Warn("account_token_guard_invalidate_token_failed", "account_id", account.ID, "error", err)
		}
	}
	return true
}

func (s *AccountTokenGuardService) reloginAccount(ctx context.Context, cfg AccountTokenGuardConfig, account *Account) (string, error) {
	entry, ok := findGuardReloginAccount(cfg, account.Name)
	if !ok {
		return "自动重登", errors.New("缺少该账号的重登凭据，请在凭证守护页面补充")
	}
	credential, err := s.relogin(ctx, cfg, entry)
	if err != nil {
		return "自动重登", err
	}
	payload := make(map[string]any, len(account.Credentials)+len(credential))
	for key, value := range account.Credentials {
		payload[key] = value
	}
	for key, value := range credential {
		payload[key] = value
	}
	input := &UpdateAccountInput{Credentials: payload}
	if strings.EqualFold(guardText(credential["plan_type"]), "free") && account.Extra["openai_excel_bps"] == true {
		// Free accounts cannot use BPS. Disable the old flag in the same write
		// so account validation does not reject the refreshed credentials.
		input.Extra = maps.Clone(account.Extra)
		input.Extra["openai_excel_bps"] = false
	}
	if _, err := s.admin.UpdateAccount(ctx, account.ID, input); err != nil {
		return "自动重登", fmt.Errorf("写回凭据失败: %w", err)
	}
	action := "重登并写回新凭据"
	refreshed, err := s.accounts.GetByID(ctx, account.ID)
	if err != nil || refreshed == nil {
		refreshed = account
	}
	if _, err := s.admin.ClearAccountError(ctx, account.ID); err != nil {
		return action, fmt.Errorf("清除错误态失败: %w", err)
	}
	if cfg.RestoreSchedulable {
		if _, err := s.admin.SetAccountSchedulable(ctx, account.ID, true); err != nil {
			return action, fmt.Errorf("恢复调度失败: %w", err)
		}
		action += "、恢复调度"
	}
	if s.invalidator != nil {
		if err := s.invalidator.InvalidateToken(ctx, refreshed); err != nil {
			slog.Warn("account_token_guard_relogin_invalidate_failed", "account_id", account.ID, "error", err)
		}
	}
	return action, nil
}

func findGuardReloginAccount(cfg AccountTokenGuardConfig, accountName string) (AccountTokenGuardReloginAccount, bool) {
	name := strings.ToLower(strings.TrimSpace(accountName))
	if name == "" {
		return AccountTokenGuardReloginAccount{}, false
	}
	for _, entry := range cfg.ReloginAccounts {
		if entry.Email == name {
			return entry, true
		}
	}
	return AccountTokenGuardReloginAccount{}, false
}

// probe 用账号当前的 access_token 调测活接口。
func (s *AccountTokenGuardService) probe(ctx context.Context, cfg AccountTokenGuardConfig, account *Account) AccountTokenGuardProbeResult {
	// A probe provider's own 429, 401 or outage must not prevent recovery
	// when a business request already established that this token was revoked.
	if guardAccountHasAuthFailure(account) {
		diagnostic := AccountTokenGuardDiagnostic{Code: "account_auth_rejected"}
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeAuth, Detail: formatGuardDiagnostic(diagnostic), Diagnostic: diagnostic}
	}
	token := strings.TrimSpace(account.GetCredential("access_token"))
	if token == "" {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeAuth, Detail: `{"code":"missing_access_token"}`, Diagnostic: AccountTokenGuardDiagnostic{Code: "missing_access_token"}}
	}
	body, err := json.Marshal(map[string]any{"access_token": token, "model": cfg.ProbeModel})
	if err != nil {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: `{"code":"request_encode_failed"}`, Diagnostic: AccountTokenGuardDiagnostic{Code: "request_encode_failed"}}
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.ProbeTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, cfg.ProbeEndpoint, bytes.NewReader(body))
	if err != nil {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: `{"code":"request_build_failed"}`, Diagnostic: AccountTokenGuardDiagnostic{Code: "request_build_failed"}}
	}
	applyGuardRequestHeaders(req, cfg.ProbeEndpoint, "probe", cfg.ProbeHeaders)
	started := time.Now()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		code := guardHTTPErrorCode(probeCtx, err)
		diagnostic := AccountTokenGuardDiagnostic{Code: code, EndedAt: guardTimePtr(time.Now())}
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: formatGuardDiagnostic(diagnostic), Diagnostic: diagnostic}
	}
	defer func() { _ = resp.Body.Close() }()
	latency := int(time.Since(started).Milliseconds())
	headersAt := time.Now()
	diagnostic := AccountTokenGuardDiagnostic{HTTPStatus: resp.StatusCode, HeadersAt: &headersAt}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		diagnostic.Code = "upstream_http_error"
		diagnostic.EndedAt = guardTimePtr(time.Now())
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: formatGuardDiagnostic(diagnostic), LatencyMS: latency, Diagnostic: diagnostic}
	}
	result, readDiag, err := readGuardResult(probeCtx, resp.Body, 2<<20, 30*time.Second)
	diagnostic = mergeGuardDiagnostic(diagnostic, readDiag)
	if err != nil {
		if diagnostic.Code == "" {
			diagnostic.Code = guardReadErrorCode(err)
		}
		diagnostic.EndedAt = guardTimePtr(time.Now())
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: formatGuardDiagnostic(diagnostic), LatencyMS: latency, Diagnostic: diagnostic}
	}
	status := strings.ToLower(guardText(result["status"]))
	errorObject, _ := result["error"].(map[string]any)
	code := strings.ToLower(guardText(errorObject["code"]))
	if result["error"] == nil && (status == "active" || status == "ok" || status == "success" || status == "succeeded") {
		diagnostic.Code = "ok"
		diagnostic.EndedAt = guardTimePtr(time.Now())
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeOK, Detail: formatGuardDiagnostic(diagnostic), LatencyMS: latency, Diagnostic: diagnostic}
	}
	if isGuardAuthCode(code) || isGuardAuthCode(status) {
		diagnostic.Code = code
		if diagnostic.Code == "" {
			diagnostic.Code = status
		}
		diagnostic.EndedAt = guardTimePtr(time.Now())
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeAuth, Detail: formatGuardDiagnostic(diagnostic), LatencyMS: latency, Diagnostic: diagnostic}
	}
	diagnostic.Code = firstNonEmptyGuard(code, status, "unexpected_result")
	diagnostic.EndedAt = guardTimePtr(time.Now())
	return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: formatGuardDiagnostic(diagnostic), LatencyMS: latency, Diagnostic: diagnostic}
}

func guardAccountHasAuthFailure(account *Account) bool {
	if account.Platform != PlatformOpenAI || !account.IsOAuth() || account.Status != StatusError {
		return false
	}
	if strings.HasPrefix(account.ErrorMessage, "Token revoked (401):") ||
		strings.HasPrefix(account.ErrorMessage, "Unauthorized (401):") {
		return true
	}
	// The account test path persists the structured upstream body under this
	// prefix. Require an explicit auth code, never a substring in its message.
	raw, ok := strings.CutPrefix(account.ErrorMessage, "Authentication failed (401):")
	if !ok {
		return false
	}
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal([]byte(raw), &result) == nil && isGuardAuthCode(result.Error.Code)
}

func (s *AccountTokenGuardService) relogin(ctx context.Context, cfg AccountTokenGuardConfig, entry AccountTokenGuardReloginAccount) (map[string]any, error) {
	payload := map[string]any{
		"action":     "start",
		"email":      entry.Email,
		"auth_mode":  "password_2fa",
		"password":   entry.Password,
		"mfa_secret": entry.MFASecret,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("重登请求构造失败")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.ReloginEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applyGuardRequestHeaders(req, cfg.ReloginEndpoint, "relogin", cfg.ReloginHeaders)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, errors.New(guardHTTPErrorCode(reqCtx, err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("重登接口返回 HTTP %d", resp.StatusCode)
	}
	result, _, err := readGuardResult(reqCtx, resp.Body, 8<<20, 60*time.Second)
	if err != nil {
		return nil, errors.New(guardReadErrorCode(err))
	}
	if credential, ok := result["credential"].(map[string]any); ok {
		if guardText(credential["access_token"]) == "" || guardText(credential["refresh_token"]) == "" || guardText(credential["id_token"]) == "" {
			return nil, errors.New("重登返回的凭据不完整")
		}
		// Re-login providers may return only tokens, leaving the stored plan_type
		// stale when credentials are merged. As in the normal OAuth flow, prefer
		// the new ID token's explicit plan (including Free) over provider metadata.
		// Missing/unparseable claims must not turn an unknown plan into Free.
		if claims, err := openai.ParseIDToken(guardText(credential["id_token"])); err == nil {
			if plan := strings.TrimSpace(claims.GetUserInfo().PlanType); plan != "" {
				credential["plan_type"] = plan
			}
		}
		return credential, nil
	}
	if errorValue, ok := result["error"].(map[string]any); ok {
		code := strings.ToLower(guardText(errorValue["code"]))
		if code == "" {
			code = "relogin_rejected"
		}
		return nil, errors.New(code)
	}
	return nil, errors.New("重登未返回凭据")
}

// applyGuardRequestHeaders 写入通用请求头，并叠加管理员配置的接口专属头部。
// 不同测活 / 重登服务的协议要求由配置提供，代码里不内置任何第三方标识。
func applyGuardRequestHeaders(req *http.Request, endpoint, kind string, extra map[string]string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson, application/json")
	origin := guardOrigin(endpoint)
	if origin != "" {
		req.Header.Set("Origin", origin)
		req.Header.Set("Referer", origin+"/")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0")
	for name, value := range extra {
		// 支持 {{uuid}} 占位符：每次请求生成新的随机值，便于需要客户端标识的接口。
		if strings.Contains(value, "{{uuid}}") {
			value = strings.ReplaceAll(value, "{{uuid}}", guardRandomID())
		}
		req.Header.Set(name, value)
	}
}

func normalizeGuardHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for name, value := range in {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		if len(out) >= 20 {
			break
		}
		out[name] = truncateGuardText(value, 512)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateGuardHeaders(in map[string]string, field string) error {
	for name, value := range in {
		if !validGuardHeaderName(name) {
			return fmt.Errorf("%s 包含非法请求头名称: %s", field, name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s 的请求头 %s 不能包含换行", field, name)
		}
	}
	return nil
}

func validGuardHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

func guardOrigin(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Scheme + "://" + parsed.Host
	}
	return raw
}

func guardRandomID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer[:4]) + "-" + hex.EncodeToString(buffer[4:6]) + "-" + hex.EncodeToString(buffer[6:8]) +
		"-" + hex.EncodeToString(buffer[8:10]) + "-" + hex.EncodeToString(buffer[10:])
}

type guardCountingReader struct {
	mu        sync.Mutex
	reader    io.Reader
	bytesRead int64
	firstByte *time.Time
}

func (r *guardCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.bytesRead += int64(n)
		if r.firstByte == nil {
			now := time.Now()
			r.firstByte = &now
		}
	}
	return n, err
}

type guardDecodeResult struct {
	event map[string]any
	err   error
}

// readGuardResult incrementally decodes ordinary JSON and NDJSON alike. It
// returns as soon as a terminal result object is complete; it does not wait for
// the upstream connection to send EOF.
func readGuardResult(ctx context.Context, body io.ReadCloser, maxBytes int64, idleTimeout time.Duration) (map[string]any, AccountTokenGuardDiagnostic, error) {
	counter := &guardCountingReader{reader: io.LimitReader(body, maxBytes+1)}
	decoder := json.NewDecoder(bufio.NewReader(counter))
	diagnostic := AccountTokenGuardDiagnostic{}
	for {
		resultCh := make(chan guardDecodeResult, 1)
		go func() {
			var event map[string]any
			err := decoder.Decode(&event)
			resultCh <- guardDecodeResult{event: event, err: err}
		}()
		timer := time.NewTimer(idleTimeout)
		var decoded guardDecodeResult
		select {
		case decoded = <-resultCh:
			if !timer.Stop() {
				<-timer.C
			}
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = body.Close()
			return nil, guardDiagnosticBytes(diagnostic, counter), ctx.Err()
		case <-timer.C:
			_ = body.Close()
			return nil, guardDiagnosticBytes(diagnostic, counter), errors.New("guard response idle timeout")
		}
		diagnostic = guardDiagnosticBytes(diagnostic, counter)
		if decoded.err != nil {
			if errors.Is(decoded.err, io.EOF) {
				return nil, diagnostic, errors.New("guard response missing result")
			}
			if counter.bytesRead > maxBytes {
				return nil, diagnostic, errors.New("guard response too large")
			}
			return nil, diagnostic, errors.New("guard response malformed")
		}
		if counter.bytesRead > maxBytes {
			return nil, diagnostic, errors.New("guard response too large")
		}
		if payload, ok := guardResultPayload(decoded.event); ok {
			now := time.Now()
			diagnostic.ResultAt = &now
			diagnostic.Code = "result"
			return payload, guardDiagnosticBytes(diagnostic, counter), nil
		}
	}
}

func guardResultPayload(event map[string]any) (map[string]any, bool) {
	if guardText(event["type"]) == "result" {
		payload, ok := event["payload"].(map[string]any)
		return payload, ok
	}
	// The relogin/probe endpoint may return one ordinary JSON object rather
	// than an event envelope. Heartbeats and progress events do not match this
	// shape and are therefore ignored.
	if status, hasStatus := event["status"]; hasStatus {
		switch strings.ToLower(guardText(status)) {
		case "active", "ok", "success", "succeeded", "failed", "error", "auth_failed":
			return event, true
		}
	}
	if _, hasCredential := event["credential"]; hasCredential {
		return event, true
	}
	if _, hasError := event["error"]; hasError {
		return event, true
	}
	return nil, false
}

func guardDiagnosticBytes(d AccountTokenGuardDiagnostic, reader *guardCountingReader) AccountTokenGuardDiagnostic {
	reader.mu.Lock()
	bytesRead, firstByte := reader.bytesRead, reader.firstByte
	reader.mu.Unlock()
	d.BytesRead = bytesRead
	if d.FirstByteAt == nil {
		d.FirstByteAt = firstByte
	}
	return d
}

func mergeGuardDiagnostic(base, read AccountTokenGuardDiagnostic) AccountTokenGuardDiagnostic {
	if read.Code != "" {
		base.Code = read.Code
	}
	if read.BytesRead > 0 {
		base.BytesRead = read.BytesRead
	}
	if read.FirstByteAt != nil {
		base.FirstByteAt = read.FirstByteAt
	}
	if read.ResultAt != nil {
		base.ResultAt = read.ResultAt
	}
	if read.EndedAt != nil {
		base.EndedAt = read.EndedAt
	}
	return base
}

func formatGuardDiagnostic(d AccountTokenGuardDiagnostic) string {
	raw, err := json.Marshal(d)
	if err != nil {
		return `{"code":"diagnostic_encode_failed"}`
	}
	return truncateGuardText(string(raw), 1000)
}

func withGuardJobID(detail, jobID string) string {
	if jobID == "" || strings.TrimSpace(detail) == "" {
		return detail
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		return detail
	}
	payload["job_id"] = jobID
	raw, err := json.Marshal(payload)
	if err != nil {
		return detail
	}
	return truncateGuardText(string(raw), 1000)
}

func guardTimePtr(value time.Time) *time.Time {
	return &value
}

func guardHTTPErrorCode(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "request_canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "request_timeout"
	}
	return "connection_error"
}

func guardReadErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "request_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "request_timeout"
	case strings.Contains(err.Error(), "idle timeout"):
		return "idle_timeout"
	case strings.Contains(err.Error(), "too large"):
		return "response_too_large"
	case strings.Contains(err.Error(), "malformed"):
		return "malformed_response"
	case strings.Contains(err.Error(), "missing result"):
		return "missing_result"
	default:
		return "response_read_error"
	}
}

func isGuardAuthCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "auth_failed", "invalid_token", "token_invalid", "requires_relogin", "invalid_grant", "revoked",
		"token_invalidated", "token_revoked", "refresh_token_reused", "refresh_token_invalidated":
		return true
	default:
		return false
	}
}

func sanitizeGuardError(err error) string {
	if err == nil {
		return ""
	}
	return truncateGuardText(err.Error(), 160)
}

func (s *AccountTokenGuardService) notify(cfg AccountTokenGuardConfig, title, body string, enabled bool) {
	if !enabled || strings.TrimSpace(cfg.BarkKey) == "" {
		return
	}
	payload := map[string]any{
		"device_key": cfg.BarkKey, "title": title, "body": truncateGuardText(body, 180),
		"group": "codex", "level": "timeSensitive", "sound": "bell", "isArchive": 1,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.day.app/push", bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func guardText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func truncateGuardText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func firstNonEmptyGuard(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
