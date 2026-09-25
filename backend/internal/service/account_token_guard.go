package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	Probed     int   `json:"probed"`
	Healthy    int   `json:"healthy"`
	AuthFailed int   `json:"auth_failed"`
	Transient  int   `json:"transient"`
	Repaired   int   `json:"repaired"`
	StateFixed int   `json:"state_fixed"`
	Failed     int   `json:"failed"`
	DurationMS int64 `json:"duration_ms"`
	StartedAt  int64 `json:"started_at"`
}

// AccountTokenGuardRuntime 是页头展示的运行信息。
type AccountTokenGuardRuntime struct {
	Running     bool                   `json:"running"`
	LastRun     *time.Time             `json:"last_run"`
	LastMessage string                 `json:"last_message"`
	Stats       AccountTokenGuardStats `json:"stats"`
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
	ListByGroup(ctx context.Context, groupID int64) ([]Account, error)
	ListByPlatform(ctx context.Context, platform string) ([]Account, error)
	ClearError(ctx context.Context, id int64) error
	SetSchedulable(ctx context.Context, id int64, schedulable bool) error
}

// AccountTokenGuardProbeResult 是单个账号的探活结果。
type AccountTokenGuardProbeResult struct {
	State     string
	Detail    string
	LatencyMS int
}

// AccountTokenGuardService 负责凭证巡检与修复。
type AccountTokenGuardService struct {
	settings    SettingRepository
	repo        AccountTokenGuardRepository
	accounts    accountTokenGuardAccounts
	admin       AdminService
	invalidator TokenCacheInvalidator
	httpClient  *http.Client

	config atomic.Value

	runMu     sync.Mutex
	stateMu   sync.Mutex
	lifecycle sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup

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
		httpClient: &http.Client{Timeout: 10 * time.Minute},
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

// Start 启动后台巡检循环；重复调用无副作用。
func (s *AccountTokenGuardService) Start() {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
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
				runCtx, runCancel := context.WithTimeout(ctx, 30*time.Minute)
				if _, err := s.RunCycle(runCtx, false); err != nil && runCtx.Err() == nil {
					slog.Debug("account_token_guard_cycle_skipped", "reason", err.Error())
				}
				runCancel()
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
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.cancel = nil
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
	defer s.stateMu.Unlock()
	info := AccountTokenGuardRuntime{LastMessage: s.lastMessage, Stats: s.stats}
	info.Running = s.cycleRunning.Load()
	if startedAt := s.cycleStartedAt.Load(); startedAt > 0 {
		started := time.Unix(startedAt, 0)
		info.LastRun = &started
	} else if !s.lastRun.IsZero() {
		last := s.lastRun
		info.LastRun = &last
	}
	return info
}

// RunCycle 执行一轮巡检；manual 仅用于日志措辞。
func (s *AccountTokenGuardService) RunCycle(ctx context.Context, manual bool) (AccountTokenGuardStats, error) {
	if !s.runMu.TryLock() {
		return s.currentStats(), errors.New("上一轮巡检仍在进行")
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
	cycleCtx, cancelCycle := context.WithTimeout(ctx, cycleLimit)
	defer cancelCycle()
	accounts, err := s.listAccounts(cycleCtx, cfg)
	if err != nil {
		s.finishCycle(started, AccountTokenGuardStats{}, "巡检失败："+err.Error())
		return s.currentStats(), err
	}
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
			results[position] = result
			// 探活完成即刻落库，页面无需等整轮结束即可看到进度。
			now := time.Now()
			_ = s.repo.UpsertState(ctx, AccountTokenGuardState{
				AccountID: accounts[position].ID, AccountName: accounts[position].Name,
				AccountStatus: accounts[position].Status, Schedulable: accounts[position].Schedulable,
				ProbeState: result.State, ProbeDetail: result.Detail, LatencyMS: result.LatencyMS,
				LastProbeAt: &now, UpdatedAt: now,
			})
		}(index)
	}
	wg.Wait()

	stats := AccountTokenGuardStats{Probed: len(accounts), StartedAt: started.Unix()}
	states := make([]AccountTokenGuardState, 0, len(accounts))
	existing := s.statesMap(ctx)
	for index := range accounts {
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
				if s.recoverState(ctx, account, cfg) {
					stats.StateFixed++
					state.LastFixAt = &now
					state.LastFixAction = "状态自愈"
					state.LastFixResult = "清除错误态并恢复调度"
					s.recordEvent(ctx, account, AccountTokenGuardEventStateFixed, "探活正常但账号处于 error 态，已清除错误并恢复调度", result.LatencyMS)
					s.notify(cfg, "凭证守护：已恢复账号调度", fmt.Sprintf("%s(#%d) 令牌有效但被禁用，已自动恢复调度", account.Name, account.ID), cfg.NotifyOnFix)
				} else {
					stats.Failed++
					state.LastFixAt = &now
					state.LastFixAction = "状态自愈"
					state.LastFixResult = "恢复失败"
					s.recordEvent(ctx, account, AccountTokenGuardEventStateFail, "探活正常但账号 error 态恢复失败", result.LatencyMS)
					s.notify(cfg, "凭证守护：状态恢复失败", fmt.Sprintf("%s(#%d) 错误态恢复失败，请检查日志", account.Name, account.ID), cfg.NotifyOnFail)
				}
			}
		case AccountTokenGuardProbeAuth:
			stats.AuthFailed++
			state.FailStreak++
			s.recordEvent(ctx, account, AccountTokenGuardEventProbeAuth, result.Detail, result.LatencyMS)
			if state.FailStreak >= cfg.FailStreakThreshold && cfg.AutoRelogin {
				action, fixErr := s.reloginAccount(ctx, cfg, account)
				if fixErr != nil {
					stats.Failed++
					state.LastFixAt = &now
					state.LastFixAction = "自动重登"
					state.LastFixResult = "失败: " + fixErr.Error()
					s.recordEvent(ctx, account, AccountTokenGuardEventReloginFail, fixErr.Error(), 0)
					s.notify(cfg, "凭证守护：自动重登失败", fmt.Sprintf("%s(#%d) 重登失败：%s", account.Name, account.ID, truncateGuardText(fixErr.Error(), 120)), cfg.NotifyOnFail)
				} else {
					stats.Repaired++
					state.FailStreak = 0
					state.LastFixAt = &now
					state.LastFixAction = "自动重登"
					state.LastFixResult = action
					s.recordEvent(ctx, account, AccountTokenGuardEventReloginOK, action, 0)
					s.notify(cfg, "凭证守护：已自动重登", fmt.Sprintf("%s(#%d) 已重登并恢复调度", account.Name, account.ID), cfg.NotifyOnFix)
				}
			}
		default:
			stats.Transient++
			s.recordEvent(ctx, account, AccountTokenGuardEventProbeTemp, result.Detail, result.LatencyMS)
		}
		states = append(states, state)
		if err := s.repo.UpsertState(ctx, state); err != nil {
			slog.Warn("account_token_guard_state_upsert_failed", "account_id", account.ID, "error", err)
		}
	}
	stats.DurationMS = time.Since(started).Milliseconds()
	message := fmt.Sprintf("巡检 %d 个账号：正常 %d，令牌失效 %d（已重登 %d），状态自愈 %d，临时异常 %d，失败 %d，耗时 %.1fs",
		stats.Probed, stats.Healthy, stats.AuthFailed, stats.Repaired, stats.StateFixed, stats.Transient, stats.Failed, float64(stats.DurationMS)/1000)
	if manual {
		s.recordEvent(context.WithoutCancel(ctx), nil, AccountTokenGuardEventManual, message, 0)
	}
	s.persistStates(ctx, states)
	s.finishCycle(started, stats, message)
	slog.Info("account_token_guard_cycle_done", "probed", stats.Probed, "healthy", stats.Healthy,
		"auth_failed", stats.AuthFailed, "repaired", stats.Repaired, "state_fixed", stats.StateFixed)
	return stats, nil
}

func (s *AccountTokenGuardService) currentStats() AccountTokenGuardStats {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.stats
}

func (s *AccountTokenGuardService) finishCycle(started time.Time, stats AccountTokenGuardStats, message string) {
	s.stateMu.Lock()
	s.lastRun = started
	s.stats = stats
	s.lastMessage = message
	s.stateMu.Unlock()
}

func (s *AccountTokenGuardService) listAccounts(ctx context.Context, cfg AccountTokenGuardConfig) ([]Account, error) {
	seen := map[int64]bool{}
	out := make([]Account, 0, 32)
	appendAccount := func(account Account) {
		if seen[account.ID] || !account.IsOAuth() || account.Platform != PlatformOpenAI || account.IsShadow() {
			return
		}
		seen[account.ID] = true
		out = append(out, account)
	}
	if len(cfg.GroupIDs) > 0 {
		for _, groupID := range cfg.GroupIDs {
			items, err := s.accounts.ListByGroup(ctx, groupID)
			if err != nil {
				return nil, err
			}
			for _, account := range items {
				appendAccount(account)
			}
		}
	} else {
		items, err := s.accounts.ListByPlatform(ctx, PlatformOpenAI)
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

func (s *AccountTokenGuardService) persistStates(ctx context.Context, states []AccountTokenGuardState) {
	ids := make([]int64, 0, len(states))
	for _, state := range states {
		ids = append(ids, state.AccountID)
		if err := s.repo.UpsertState(ctx, state); err != nil {
			slog.Warn("account_token_guard_state_upsert_failed", "account_id", state.AccountID, "error", err)
		}
	}
	if len(ids) > 0 {
		if err := s.repo.DeleteStatesExcept(ctx, ids); err != nil {
			slog.Warn("account_token_guard_state_prune_failed", "error", err)
		}
	}
	if err := s.repo.PruneEvents(ctx, time.Now().Add(-14*24*time.Hour)); err != nil {
		slog.Warn("account_token_guard_event_prune_failed", "error", err)
	}
}

func (s *AccountTokenGuardService) recordEvent(ctx context.Context, account *Account, kind, detail string, latencyMS int) {
	event := AccountTokenGuardEvent{Kind: kind, Detail: truncateGuardText(detail, 1000), LatencyMS: latencyMS}
	if account != nil {
		event.AccountID = account.ID
		event.AccountName = account.Name
	}
	if err := s.repo.RecordEvent(ctx, event); err != nil {
		slog.Warn("account_token_guard_event_record_failed", "kind", kind, "error", err)
	}
}

// ReloginAccount 供页面手动触发单个账号重登。
func (s *AccountTokenGuardService) ReloginAccount(ctx context.Context, accountID int64) (string, error) {
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
	_ = s.repo.UpsertState(ctx, state)
	s.recordEvent(ctx, account, AccountTokenGuardEventReloginOK, "手动重登: "+action, 0)
	return action, nil
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
	if _, err := s.admin.UpdateAccount(ctx, account.ID, &UpdateAccountInput{Credentials: payload}); err != nil {
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
	token := strings.TrimSpace(account.GetCredential("access_token"))
	if token == "" {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeAuth, Detail: "账号没有 access_token"}
	}
	body, err := json.Marshal(map[string]any{"access_token": token, "model": cfg.ProbeModel})
	if err != nil {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: "构造探活请求失败"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.ProbeTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, cfg.ProbeEndpoint, bytes.NewReader(body))
	if err != nil {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: "构造探活请求失败"}
	}
	applyGuardRequestHeaders(req, cfg.ProbeEndpoint, "probe", cfg.ProbeHeaders)
	started := time.Now()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: "探活请求失败: " + truncateGuardText(err.Error(), 140)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: "探活响应读取失败", LatencyMS: latency}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeAuth, Detail: "探活返回 401", LatencyMS: latency}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient,
			Detail: fmt.Sprintf("探活接口 %d: %s", resp.StatusCode, truncateGuardText(strings.TrimSpace(string(raw)), 160)), LatencyMS: latency}
	}
	result, err := parseGuardNDJSONResult(raw)
	if err != nil {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: "探活响应缺少 result 事件", LatencyMS: latency}
	}
	status := strings.ToLower(guardText(result["status"]))
	errorObject, _ := result["error"].(map[string]any)
	code := strings.ToLower(guardText(errorObject["code"]))
	message := guardText(errorObject["message"])
	if result["error"] == nil && (status == "active" || status == "ok" || status == "success" || status == "succeeded") {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeOK, Detail: fmt.Sprintf("active %dms", latency), LatencyMS: latency}
	}
	blob := strings.ToLower(code + " " + message + " " + guardText(result["status"]))
	if containsGuardAny(blob, []string{"auth_failed", "invalid_token", "token_invalid", "unauthorized", "requires_relogin", "invalid_grant", "revoked", "oauth token failure"}) {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeAuth, Detail: truncateGuardText(firstNonEmptyGuard(code, status, message), 160), LatencyMS: latency}
	}
	if containsGuardAny(blob, []string{"rate_limited", "quota", "capacity", "timeout", "busy", "pending_limit", "overload", "too many", "probe_rejected", "未完成", "未返回", "incomplete"}) {
		return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: truncateGuardText(firstNonEmptyGuard(code, status, message), 160), LatencyMS: latency}
	}
	return AccountTokenGuardProbeResult{State: AccountTokenGuardProbeTransient, Detail: truncateGuardText(firstNonEmptyGuard(status, code, message, "unknown"), 160), LatencyMS: latency}
}

// relogin 调用站点重登接口，返回新的凭据集合。
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
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.ReloginEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applyGuardRequestHeaders(req, cfg.ReloginEndpoint, "relogin", cfg.ReloginHeaders)
	client := &http.Client{Timeout: 25 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("重登接口 %d: %s", resp.StatusCode, truncateGuardText(strings.TrimSpace(string(raw)), 160))
	}
	result, err := parseGuardNDJSONResult(raw)
	if err != nil {
		return nil, err
	}
	if credential, ok := result["credential"].(map[string]any); ok {
		if guardText(credential["access_token"]) == "" || guardText(credential["refresh_token"]) == "" || guardText(credential["id_token"]) == "" {
			return nil, errors.New("重登返回的凭据不完整")
		}
		return credential, nil
	}
	if errorValue, ok := result["error"].(map[string]any); ok {
		return nil, fmt.Errorf("%s: %s", guardText(errorValue["code"]), truncateGuardText(guardText(errorValue["message"]), 160))
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

func parseGuardNDJSONResult(raw []byte) (map[string]any, error) {
	var result map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if guardText(event["type"]) != "result" {
			continue
		}
		if payload, ok := event["payload"].(map[string]any); ok {
			result = payload
		}
	}
	if result == nil {
		return nil, errors.New("响应缺少 result 事件")
	}
	return result, nil
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

func containsGuardAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func firstNonEmptyGuard(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
