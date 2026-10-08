package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"
)

type AccountOpsUsageWindow struct {
	Window      string     `json:"window"`
	Label       string     `json:"label"`
	UsedPercent *float64   `json:"used_percent"`
	Status      string     `json:"status"`
	ObservedAt  *time.Time `json:"observed_at,omitempty"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}
type AccountOpsBalanceAccount struct {
	AccountID     int64                   `json:"account_id"`
	AccountName   string                  `json:"account_name"`
	Platform      string                  `json:"platform"`
	Type          string                  `json:"type"`
	Balance       *float64                `json:"balance"`
	Unit          string                  `json:"unit"`
	BalanceStatus string                  `json:"balance_status"`
	ReceivedAt    *time.Time              `json:"received_at"`
	UsageWindows  []AccountOpsUsageWindow `json:"usage_windows"`
}

func opsNumber(v any) (float64, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case float32:
		n = float64(x)
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	case json.Number:
		var err error
		n, err = x.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}
func opsTime(v any) *time.Time {
	if t, ok := v.(time.Time); ok {
		return &t
	}
	if t, ok := v.(*time.Time); ok {
		return t
	}
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return &t
		}
	}
	if n, ok := opsNumber(v); ok && n > 0 && n < 253402300800 {
		t := time.Unix(int64(n), 0)
		return &t
	}
	return nil
}
func opsFresh(observed, until *time.Time, now time.Time) bool {
	return observed != nil && until != nil && !observed.IsZero() && !observed.After(now) && until.After(*observed) && until.After(now)
}
func opsBalanceAccount(a *Account, now time.Time) AccountOpsBalanceAccount {
	item := AccountOpsBalanceAccount{AccountID: a.ID, AccountName: a.Name, Platform: a.Platform, Type: a.Type, Unit: "USD", BalanceStatus: "unknown", UsageWindows: []AccountOpsUsageWindow{}}
	if a.Type != AccountTypeAPIKey {
		item.Unit = "%"
		return item
	}
	snap := decodeUpstreamBillingProbeSnapshot(a.Extra)
	if snap == nil || snap.Balance == nil {
		return item
	}
	b := snap.Balance
	item.BalanceStatus = b.Status
	item.ReceivedAt = b.ReceivedAt
	if unit, ok := b.Data["unit"].(string); ok && unit != "" {
		item.Unit = unit
	}
	if b.Status != "ok" {
		return item
	}
	if !opsFresh(b.ReceivedAt, b.FreshUntil, now) {
		item.BalanceStatus = "stale"
		return item
	}
	if unlimited, ok := b.Data["unlimited"].(bool); ok && unlimited {
		item.BalanceStatus = "unsupported"
		return item
	}
	n, ok := opsNumber(b.Data["remaining"])
	if !ok {
		item.BalanceStatus = "unsupported"
		return item
	}
	item.Balance = &n
	return item
}
func opsWindow(window, label string, value any, scale float64, observed, reset *time.Time, now time.Time) AccountOpsUsageWindow {
	w := AccountOpsUsageWindow{Window: window, Label: label, Status: "unknown", ObservedAt: observed, ResetsAt: reset}
	n, ok := opsNumber(value)
	if !ok || n < 0 {
		return w
	}
	n *= scale
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return w
	}
	if observed == nil || reset == nil {
		return w
	}
	if observed.After(now) || now.Sub(*observed) > 15*time.Minute || !reset.After(now) {
		w.Status = "stale"
		return w
	}
	w.Status = "ok"
	w.UsedPercent = &n
	return w
}
func opsExtraUsageWindows(a *Account, now time.Time) []AccountOpsUsageWindow {
	extra := a.Extra
	windows := []AccountOpsUsageWindow{}
	switch a.Platform {
	case PlatformOpenAI:
		observed := opsTime(extra["codex_usage_updated_at"])
		for _, window := range []string{"5h", "7d"} {
			reset := opsTime(extra["codex_"+window+"_reset_at"])
			if reset == nil && observed != nil {
				if seconds, ok := opsNumber(extra["codex_"+window+"_reset_after_seconds"]); ok && seconds > 0 {
					t := observed.Add(time.Duration(seconds * float64(time.Second)))
					reset = &t
				}
			}
			windows = append(windows, opsWindow(window, window, extra["codex_"+window+"_used_percent"], 1, observed, reset, now))
		}
	case PlatformAnthropic:
		observed := opsTime(extra["passive_usage_sampled_at"])
		windows = append(windows, opsWindow("5h", "5h", extra["session_window_utilization"], 100, observed, a.SessionWindowEnd, now))
		for _, window := range []string{"7d", "7d_oi"} {
			windows = append(windows, opsWindow(window, window, extra["passive_usage_"+window+"_utilization"], 100, observed, opsTime(extra["passive_usage_"+window+"_reset"]), now))
		}
	default:
		windows = append(windows, AccountOpsUsageWindow{Window: "any", Label: "额度", Status: "unsupported"})
	}
	return windows
}
func (s *AccountOpsService) SetNotificationUsageCache(cache *UsageCache) { s.usageCache = cache }
func (s *AccountOpsService) usageWindows(ctx context.Context, a *Account, now time.Time) []AccountOpsUsageWindow {
	windows := opsExtraUsageWindows(a, now)
	if a.Platform == PlatformGemini && s.geminiQuota != nil && s.usageLogs != nil {
		reader := AccountUsageService{geminiQuotaService: s.geminiQuota, usageLogRepo: s.usageLogs}
		info, err := reader.getGeminiUsage(ctx, a)
		if err != nil {
			return []AccountOpsUsageWindow{{Window: "any", Label: "额度", Status: "failed"}}
		}
		return opsInfoWindows(info, now)
	}
	if a.Platform == PlatformGrok {
		billing, err := grokBillingSnapshotFromExtra(a.Extra)
		if err == nil && billing != nil {
			info := &UsageInfo{UpdatedAt: opsTime(billing.UpdatedAt)}
			applyGrokBillingProgressWindows(info, billing, now)
			windows = opsInfoWindows(info, now)
			if billing.StatusCode >= 400 {
				for i := range windows {
					windows[i].Status = "failed"
					windows[i].UsedPercent = nil
				}
			}
			return windows
		}
	}
	if s.usageCache == nil {
		return windows
	}
	if a.Platform == PlatformAnthropic {
		if v, ok := s.usageCache.apiCache.Load(a.ID); ok {
			if cached, ok := v.(*apiUsageCache); ok && cached.err == nil && cached.response != nil && now.Sub(cached.timestamp) <= 15*time.Minute {
				r := cached.response
				windows = []AccountOpsUsageWindow{}
				for _, entry := range []struct {
					key    string
					window ClaudeUsageWindow
				}{{"5h", r.FiveHour}, {"7d", r.SevenDay}, {"7d_sonnet", r.SevenDaySonnet}, {"7d_oi", r.SevenDayOverageIncluded}} {
					windows = append(windows, opsWindow(entry.key, entry.key, entry.window.Utilization, 1, &cached.timestamp, opsTime(entry.window.ResetsAt), now))
				}
			}
		}
	}
	if a.Platform == PlatformAntigravity {
		if v, ok := s.usageCache.antigravityCache.Load(a.ID); ok {
			if cached, ok := v.(*antigravityUsageCache); ok && cached.usageInfo != nil {
				windows = []AccountOpsUsageWindow{}
				for key, q := range cached.usageInfo.AntigravityQuota {
					if q == nil {
						continue
					}
					windows = append(windows, opsWindow(key, key, float64(q.Utilization), 1, &cached.timestamp, opsTime(q.ResetTime), now))
				}
			}
		}
	}
	return windows
}
func (s *AccountOpsService) BalanceAccounts(ctx context.Context) ([]AccountOpsBalanceAccount, error) {
	return s.thresholdAccounts(ctx, true)
}
func (s *AccountOpsService) ThresholdAccounts(ctx context.Context) ([]AccountOpsBalanceAccount, error) {
	return s.thresholdAccounts(ctx, false)
}
func (s *AccountOpsService) thresholdAccounts(ctx context.Context, apiOnly bool) ([]AccountOpsBalanceAccount, error) {
	if s.accounts == nil {
		return nil, errors.New("account repository unavailable")
	}
	accounts, err := s.accounts.ListAllWithFilters(ctx, "", "", "", "", 0, "")
	if err != nil {
		return nil, err
	}
	items := []AccountOpsBalanceAccount{}
	now := time.Now()
	for i := range accounts {
		a := &accounts[i]
		if a.Type != AccountTypeAPIKey && (apiOnly || a.Type != AccountTypeOAuth) {
			continue
		}
		item := opsBalanceAccount(a, now)
		if a.Type == AccountTypeOAuth {
			item.UsageWindows = s.usageWindows(ctx, a, now)
		}
		items = append(items, item)
	}
	return items, nil
}
func (s *AccountOpsService) validateBalanceRules(ctx context.Context, rules []AccountOpsBalanceRule) error {
	for _, r := range rules {
		if _, err := s.balanceRuleAccount(ctx, r.AccountID); err != nil {
			return err
		}
	}
	return nil
}
func (s *AccountOpsService) balanceRuleAccount(ctx context.Context, id int64) (*Account, error) {
	if s.accounts == nil {
		return nil, errors.New("account repository unavailable")
	}
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil && !errors.Is(err, ErrAccountNotFound) {
		return nil, errors.New("balance account lookup unavailable")
	}
	if errors.Is(err, ErrAccountNotFound) || a == nil || a.Type != AccountTypeAPIKey {
		return nil, accountOpsConfigValidation("balance rules require a live API key account")
	}
	return a, nil
}
func (s *AccountOpsService) validateQuotaRules(ctx context.Context, rules []AccountOpsQuotaRule) error {
	for _, r := range rules {
		if s.accounts == nil {
			return errors.New("account repository unavailable")
		}
		a, err := s.accounts.GetByID(ctx, r.AccountID)
		if err != nil && !errors.Is(err, ErrAccountNotFound) {
			return errors.New("quota account lookup unavailable")
		}
		if errors.Is(err, ErrAccountNotFound) || a == nil || a.Type != AccountTypeOAuth {
			return accountOpsConfigValidation("quota rules require a live OAuth account")
		}
	}
	return nil
}
func opsAccountIdentity(a *Account) string {
	raw, _ := json.Marshal(struct {
		Platform, Type string
		Credentials    map[string]any
	}{a.Platform, a.Type, a.Credentials})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func opsPickWindow(windows []AccountOpsUsageWindow, selected string) *AccountOpsUsageWindow {
	var best *AccountOpsUsageWindow
	for i := range windows {
		w := &windows[i]
		if w.Status != "ok" || w.UsedPercent == nil || (selected != "any" && selected != "" && selected != w.Window) {
			continue
		}
		if best == nil || *w.UsedPercent > *best.UsedPercent {
			best = w
		}
	}
	return best
}
func (s *AccountOpsService) evaluateThreshold(ctx context.Context, a *Account, kind string, c AccountOpsConfig, now time.Time) (*AccountOpsEvent, bool) {
	e, state := s.assessThreshold(ctx, a, kind, c, now)
	return e, state == "active"
}

// Missing or stale samples cancel delivery without asserting that the account recovered.
func (s *AccountOpsService) assessThreshold(ctx context.Context, a *Account, kind string, c AccountOpsConfig, now time.Time) (*AccountOpsEvent, string) {
	if a == nil {
		return nil, "suppressed"
	}
	e := &AccountOpsEvent{AccountID: a.ID, AccountName: a.Name, Kind: kind, Signal: kind, Identity: opsAccountIdentity(a)}
	if kind == "balance_threshold" && a.Type == AccountTypeAPIKey {
		for _, r := range c.BalanceThresholds {
			if r.AccountID == a.ID && r.Enabled {
				item := opsBalanceAccount(a, now)
				if item.Balance == nil || item.Unit != r.Unit {
					return nil, "suppressed"
				}

				threshold := r.Threshold
				e.Details = &AccountOpsDetails{Balance: item.Balance, Threshold: &threshold, Unit: item.Unit, ObservedAt: item.ReceivedAt}
				if *item.Balance > r.Threshold {
					return e, "resolved"
				}
				return e, "active"
			}
		}
	}
	if kind == "quota_threshold" && a.Type == AccountTypeOAuth {
		for _, r := range c.QuotaThresholds {
			if r.AccountID == a.ID && r.Enabled {
				windows := s.usageWindows(ctx, a, now)
				window := opsPickWindow(windows, r.Window)
				if window != nil && *window.UsedPercent >= r.ThresholdPercent {
					threshold := r.ThresholdPercent
					e.Details = &AccountOpsDetails{UsedPercent: window.UsedPercent, ThresholdPercent: &threshold, Window: window.Window, ResetsAt: window.ResetsAt, ObservedAt: window.ObservedAt, Unit: "%"}
					return e, "active"
				}
				// All selected windows must have valid current samples. Expired
				// reset times never manufacture a healthy zero-percent reading.
				confirmed := false
				for _, w := range windows {
					if r.Window != "any" && r.Window != "" && r.Window != w.Window {
						continue
					}
					if w.Status != "ok" || w.UsedPercent == nil {
						return nil, "suppressed"
					}
					confirmed = true
				}
				if confirmed && window != nil {
					threshold := r.ThresholdPercent
					e.Details = &AccountOpsDetails{UsedPercent: window.UsedPercent, ThresholdPercent: &threshold, Window: window.Window, ResetsAt: window.ResetsAt, ObservedAt: window.ObservedAt, Unit: "%"}
					return e, "resolved"
				}
				return nil, "suppressed"
			}
		}
	}
	return nil, "suppressed"
}
func (s *AccountOpsService) scanBalances(ctx context.Context) {
	c := s.currentConfig()
	if !c.Enabled || s.accounts == nil {
		return
	}

	for _, kind := range []string{"balance_threshold", "quota_threshold"} {
		ids := []int64{}
		if kind == "balance_threshold" {
			for _, r := range c.BalanceThresholds {
				ids = append(ids, r.AccountID)
			}
		} else {
			for _, r := range c.QuotaThresholds {
				ids = append(ids, r.AccountID)
			}
		}
		for _, id := range ids {
			lookupTimeout := s.thresholdLookupTimeout
			if lookupTimeout <= 0 {
				lookupTimeout = 5 * time.Second
			}
			query, cancel := context.WithTimeout(ctx, lookupTimeout)
			a, err := s.accounts.GetByID(query, id)
			cancel()
			// Lookup and observation have independent budgets: a timed-out read must
			// still durably reset consecutive healthy confirmation.
			query, cancel = context.WithTimeout(ctx, 5*time.Second)
			missing := errors.Is(err, ErrAccountNotFound)
			if err != nil && !missing {
				a = nil
			}
			// Even a failed lookup resets confirmation; preserve its infrastructure
			// error separately from the monitor's unknown observation.
			observeErr := s.observeThreshold(query, a, id, kind, c, time.Now(), missing)
			if err == nil || missing {
				err = observeErr
			}
			if err != nil {
				s.failures.Add(1)
			}
			cancel()
		}
	}
}
func (s *AccountOpsService) recheckThreshold(ctx context.Context, e *AccountOpsEvent, c AccountOpsConfig) (string, error) {
	if s.accounts == nil {
		return "failed", errors.New("account repository unavailable")
	}
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	a, err := s.accounts.GetByID(query, e.AccountID)
	if errors.Is(err, ErrAccountNotFound) {
		return "suppressed", nil
	}
	if err != nil {
		return "failed", err
	}
	if a == nil || (e.Identity != "" && opsAccountIdentity(a) != e.Identity) {
		return "suppressed", nil
	}
	criteria, enabled, alert, recovery := opsCriteria(c, e.AccountID, e.Kind)
	if !enabled || (e.Criteria != "" && criteria != e.Criteria) || (e.Phase == "recovery" && !recovery) || (e.Phase != "recovery" && !alert) {
		return "suppressed", nil
	}
	if e.Phase != "" {
		if repo, ok := s.repo.(accountOpsThresholdRepository); ok {
			valid, err := repo.ThresholdEventEligible(query, e)
			if err != nil {
				return "failed", err
			}
			if !valid {
				return "suppressed", nil
			}
		}
	}
	// A recovery is an immutable past transition. A newer adverse episode does
	// not rewrite its snapshot or claim the account is currently healthy.
	if e.Phase == "recovery" {
		return "active", nil
	}
	fresh, state := s.assessThreshold(query, a, e.Kind, c, time.Now())
	if state == "active" && e.Phase == "" {
		e.Details = fresh.Details
		e.AccountName = fresh.AccountName
	}
	if e.Phase == "alert" && state != "active" {
		return "deferred", nil
	}
	return state, nil
}

func (s *AccountOpsService) SetNotificationQuotaReaders(logs UsageLogRepository, quota *GeminiQuotaService) {
	s.usageLogs = logs
	s.geminiQuota = quota
}
func opsInfoWindows(info *UsageInfo, now time.Time) []AccountOpsUsageWindow {
	windows := []AccountOpsUsageWindow{}
	if info == nil {
		return windows
	}
	for _, entry := range []struct {
		key string
		p   *UsageProgress
	}{{"5h", info.FiveHour}, {"7d", info.SevenDay}, {"30d", info.ThirtyDay}, {"gemini_shared_daily", info.GeminiSharedDaily}, {"gemini_pro_daily", info.GeminiProDaily}, {"gemini_flash_daily", info.GeminiFlashDaily}, {"gemini_shared_minute", info.GeminiSharedMinute}, {"gemini_pro_minute", info.GeminiProMinute}, {"gemini_flash_minute", info.GeminiFlashMinute}} {
		if entry.p != nil {
			windows = append(windows, opsWindow(entry.key, entry.key, entry.p.Utilization, 1, info.UpdatedAt, entry.p.ResetsAt, now))
		}
	}
	if len(windows) == 0 {
		windows = append(windows, AccountOpsUsageWindow{Window: "any", Label: "额度", Status: "unknown"})
	}
	return windows
}
