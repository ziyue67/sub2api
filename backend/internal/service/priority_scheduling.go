package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
)

const prioritySchedulingSettingKey = "priority_scheduling_v1"

// PrioritySchedulingConfig only controls freely selectable OpenAI text requests.
// Account eligibility, continuity, profit gates and concurrency acquisition stay authoritative.
type PrioritySchedulingConfig struct {
	Enabled            bool     `json:"enabled"`
	Mode               string   `json:"mode"`
	GroupIDs           []int64  `json:"group_ids"`
	Models             []string `json:"models"`
	WindowMinutes      int      `json:"window_minutes"`
	MinSamples         int      `json:"min_samples"`
	TargetTTFTMs       int      `json:"target_ttft_ms"`
	MaxLoadPercent     int      `json:"max_load_percent"`
	MinQualityPercent  int      `json:"min_quality_percent"`
	QualityMaxAgeHours int      `json:"quality_max_age_hours"`
	QualityWeight      float64  `json:"quality_weight"`
	LatencyWeight      float64  `json:"latency_weight"`
	LoadWeight         float64  `json:"load_weight"`
	CostWeight         float64  `json:"cost_weight"`
}

func DefaultPrioritySchedulingConfig() PrioritySchedulingConfig {
	return PrioritySchedulingConfig{Mode: "balanced", GroupIDs: []int64{}, Models: []string{}, WindowMinutes: 60, MinSamples: 5, TargetTTFTMs: 3000, MaxLoadPercent: 80, MinQualityPercent: 90, QualityMaxAgeHours: 24, QualityWeight: 30, LatencyWeight: 25, LoadWeight: 25, CostWeight: 20}
}

func ValidatePrioritySchedulingConfig(c PrioritySchedulingConfig) error {
	if !slices.Contains([]string{"experience", "balanced", "profit", "custom"}, c.Mode) {
		return errors.New("invalid scheduling mode")
	}
	if c.WindowMinutes < 5 || c.WindowMinutes > 1440 || c.MinSamples < 1 || c.MinSamples > 1000 || c.TargetTTFTMs < 100 || c.TargetTTFTMs > 120000 || c.MaxLoadPercent < 10 || c.MaxLoadPercent > 100 || c.MinQualityPercent < 0 || c.MinQualityPercent > 100 || c.QualityMaxAgeHours < 1 || c.QualityMaxAgeHours > 168 {
		return errors.New("scheduling thresholds outside allowed range")
	}
	if len(c.GroupIDs) > 100 || len(c.Models) > 100 {
		return errors.New("at most 100 groups or models allowed")
	}
	for _, id := range c.GroupIDs {
		if id <= 0 {
			return errors.New("group IDs must be positive")
		}
	}
	for _, model := range c.Models {
		if len(model) == 0 || len(model) > 200 {
			return errors.New("model names must contain 1–200 bytes")
		}
	}
	sum := 0.0
	for _, w := range []float64{c.QualityWeight, c.LatencyWeight, c.LoadWeight, c.CostWeight} {
		if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 || w > 100 {
			return errors.New("weights must be between 0 and 100")
		}
		sum += w
	}
	if sum <= 0 {
		return errors.New("at least one weight must be positive")
	}
	return nil
}

func (c PrioritySchedulingConfig) applies(group *int64, model string) bool {
	return c.Enabled && (len(c.GroupIDs) == 0 || group != nil && slices.Contains(c.GroupIDs, *group)) && (len(c.Models) == 0 || slices.Contains(c.Models, model))
}

func (c PrioritySchedulingConfig) weights() [4]float64 {
	switch c.Mode {
	case "experience":
		return [4]float64{40, 35, 20, 5}
	case "profit":
		return [4]float64{20, 15, 20, 45}
	case "balanced":
		return [4]float64{30, 25, 25, 20}
	default:
		return [4]float64{c.QualityWeight, c.LatencyWeight, c.LoadWeight, c.CostWeight}
	}
}

type priorityConfigCache struct {
	saveMu     sync.Mutex
	mu         sync.Mutex
	config     PrioritySchedulingConfig
	expires    time.Time
	refreshing bool
	revision   uint64
}

func (s *SettingService) GetPrioritySchedulingConfig(ctx context.Context) (PrioritySchedulingConfig, error) {
	c := DefaultPrioritySchedulingConfig()
	raw, err := s.settingRepo.GetValue(ctx, prioritySchedulingSettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	return c, ValidatePrioritySchedulingConfig(c)
}
func (s *SettingService) SavePrioritySchedulingConfig(ctx context.Context, c PrioritySchedulingConfig) error {
	if err := ValidatePrioritySchedulingConfig(c); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	cache := &s.prioritySchedulingConfig
	cache.saveMu.Lock()
	defer cache.saveMu.Unlock()
	if err = s.settingRepo.Set(ctx, prioritySchedulingSettingKey, string(raw)); err != nil {
		return err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.revision++
	cache.config = c
	cache.expires = time.Now().Add(5 * time.Second)
	return nil
}

// No database access on the request goroutine, including a cold start. A failed
// refresh disables the feature; other processes poll settings on a 5s interval.
func (s *SettingService) prioritySchedulingRuntimeConfig() PrioritySchedulingConfig {
	if s == nil || s.settingRepo == nil {
		return DefaultPrioritySchedulingConfig()
	}
	cache := &s.prioritySchedulingConfig
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	if !cache.refreshing && now.After(cache.expires) {
		cache.refreshing = true
		revision := cache.revision
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c, err := s.GetPrioritySchedulingConfig(ctx)
			cache.mu.Lock()
			defer cache.mu.Unlock()
			cache.refreshing = false
			if cache.revision != revision {
				return
			}
			if err != nil {
				c = DefaultPrioritySchedulingConfig()
			}
			cache.config = c
			cache.expires = time.Now().Add(5 * time.Second)
		}()
	}
	if now.After(cache.expires.Add(5 * time.Second)) {
		return DefaultPrioritySchedulingConfig()
	}
	return cache.config
}

func (s *OpenAIGatewayService) prioritySchedulingRuntimeConfig() PrioritySchedulingConfig {
	if s == nil {
		return DefaultPrioritySchedulingConfig()
	}
	return s.settingService.prioritySchedulingRuntimeConfig()
}

type PrioritySchedulingSignal struct {
	Revenue        float64 `json:"revenue"`
	BaseCost       float64 `json:"-"`
	ProfitSamples  int     `json:"profit_samples"`
	Samples        int     `json:"samples"`
	P90TTFTMs      float64 `json:"p90_ttft_ms"`
	QualityPassed  int     `json:"quality_passed"`
	QualitySamples int     `json:"quality_samples"`
}

type PrioritySchedulingQuery struct {
	AccountIDs   []int64
	Model        string
	GroupID      *int64
	UsageSince   time.Time
	QualitySince time.Time
}
type PrioritySchedulingSignalReader interface {
	ReadPrioritySchedulingSignals(context.Context, PrioritySchedulingQuery) (map[int64]PrioritySchedulingSignal, error)
}

type prioritySignalEntry struct {
	signals    map[int64]PrioritySchedulingSignal
	expires    time.Time
	observed   time.Time
	refreshing bool
}

type prioritySchedulingState struct {
	mu      sync.Mutex
	entries map[string]*prioritySignalEntry
	active  int
	latest  *PrioritySchedulingSnapshot
}

type PrioritySchedulingScore struct {
	TheoreticalCost float64  `json:"theoretical_cost"`
	Profit          *float64 `json:"profit"`
	Margin          *float64 `json:"margin"`
	EconomicsSource string   `json:"economics_source"`
	Priority        int      `json:"priority"`
	Concurrency     int      `json:"concurrency"`
	LoadFactor      int      `json:"load_factor"`
	AccountID       int64    `json:"account_id"`
	AccountName     string   `json:"account_name"`
	Score           float64  `json:"score"`
	Tier            string   `json:"tier"`
	Reasons         []string `json:"reasons"`
	Rate            *float64 `json:"rate"`
	LoadPercent     *int     `json:"load_percent"`
	Waiting         int      `json:"waiting"`
	PrioritySchedulingSignal
}
type PrioritySchedulingSnapshot struct {
	At           time.Time                 `json:"at"`
	Model        string                    `json:"model"`
	GroupID      *int64                    `json:"group_id"`
	Mode         string                    `json:"mode"`
	HistoryReady bool                      `json:"history_ready"`
	Candidates   []PrioritySchedulingScore `json:"candidates"`
}

func (s *OpenAIGatewayService) PrioritySchedulingSnapshot() *PrioritySchedulingSnapshot {
	if s == nil {
		return nil
	}
	s.priorityScheduling.mu.Lock()
	defer s.priorityScheduling.mu.Unlock()
	// Published snapshots are immutable.
	return s.priorityScheduling.latest
}

func (s *OpenAIGatewayService) prioritySignals(req OpenAIAccountScheduleRequest, c PrioritySchedulingConfig, accounts []openAIAccountCandidateScore) (map[int64]PrioritySchedulingSignal, bool) {
	reader, ok := s.usageLogRepo.(PrioritySchedulingSignalReader)
	if !ok {
		return nil, false
	}
	ids := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.account.ID)
	}
	slices.Sort(ids)
	// Bound both aggregation and the memory used by client-controlled model names.
	if len(ids) > 2000 || len(req.RequestedModel) > 200 {
		return nil, false
	}
	key := fmt.Sprintf("%q/%d/%d/%d/%v", req.RequestedModel, derefGroupID(req.GroupID), c.WindowMinutes, c.QualityMaxAgeHours, ids)
	state := &s.priorityScheduling
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.entries == nil {
		state.entries = make(map[string]*prioritySignalEntry)
	}
	now := time.Now()
	entry := state.entries[key]
	if entry == nil {
		if len(state.entries) >= 32 {
			for k, e := range state.entries {
				if !e.refreshing && now.After(e.expires) {
					delete(state.entries, k)
				}
			}
			if len(state.entries) >= 32 {
				return nil, false
			}
		}
		entry = &prioritySignalEntry{}
		state.entries[key] = entry
	}
	if !entry.refreshing && now.After(entry.expires) && state.active < 2 {
		entry.refreshing = true
		state.active++
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			signals, err := reader.ReadPrioritySchedulingSignals(ctx, PrioritySchedulingQuery{AccountIDs: ids, Model: req.RequestedModel, GroupID: req.GroupID, UsageSince: now.Add(-time.Duration(c.WindowMinutes) * time.Minute), QualitySince: now.Add(-time.Duration(c.QualityMaxAgeHours) * time.Hour)})
			state.mu.Lock()
			defer state.mu.Unlock()
			state.active--
			entry.refreshing = false
			entry.expires = time.Now().Add(30 * time.Second)
			if err == nil {
				entry.signals = signals
				entry.observed = time.Now()
			} else {
				entry.signals = nil
				entry.observed = time.Time{}
			}
		}()
	}
	if entry.observed.IsZero() || now.Sub(entry.observed) > time.Minute {
		return nil, false
	}
	return entry.signals, true
}

func scorePriorityCandidate(c PrioritySchedulingConfig, item openAIAccountCandidateScore, signal PrioritySchedulingSignal, _ time.Time) PrioritySchedulingScore {
	rate := item.account.CostMultiplier()
	theoreticalCost := signal.BaseCost * rate
	out := PrioritySchedulingScore{Rate: &rate, TheoreticalCost: theoreticalCost, AccountID: item.account.ID, AccountName: item.account.Name, Priority: openAIAccountSchedulingPriority(item.account), Concurrency: item.account.Concurrency, LoadFactor: item.account.EffectiveLoadFactor(), Tier: "eligible", Reasons: []string{}, PrioritySchedulingSignal: signal, Waiting: item.loadInfo.WaitingCount}
	quality, latency, load, cost := 0.5, 0.5, 0.5, 0.0
	degraded, unknown := false, false
	if signal.QualitySamples > 0 {
		quality = float64(signal.QualityPassed) / float64(signal.QualitySamples)
		if quality*100 < float64(c.MinQualityPercent) {
			degraded = true
			out.Reasons = append(out.Reasons, "quality_below_target")
		}
	} else {
		unknown = true
		out.Reasons = append(out.Reasons, "quality_unknown")
	}
	if signal.Samples >= c.MinSamples {
		latency = 1 / (1 + signal.P90TTFTMs/float64(c.TargetTTFTMs))
		if signal.P90TTFTMs > float64(c.TargetTTFTMs) {
			degraded = true
			out.Reasons = append(out.Reasons, "latency_above_target")
		}
	} else {
		unknown = true
		out.Reasons = append(out.Reasons, "latency_insufficient")
	}
	if item.loadKnown {
		percent := 0
		if item.account.Concurrency > 0 {
			percent = int(100 * float64(item.loadInfo.CurrentConcurrency) / float64(item.account.Concurrency))
		}
		out.LoadPercent = &percent
		load = (1 - clamp01(float64(item.loadInfo.CurrentConcurrency)/float64(item.account.EffectiveLoadFactor()))) / (1 + float64(max(0, item.loadInfo.WaitingCount)))
		if percent >= c.MaxLoadPercent || item.loadInfo.WaitingCount > 0 {
			degraded = true
			out.Reasons = append(out.Reasons, "busy")
		}
	} else {
		unknown = true
		out.Reasons = append(out.Reasons, "load_unknown")
	}
	if signal.ProfitSamples >= c.MinSamples && signal.Revenue >= 0 && theoreticalCost >= 0 && signal.Revenue+theoreticalCost > 0 {
		profit := signal.Revenue - theoreticalCost
		out.Profit = &profit
		if signal.Revenue > 0 {
			margin := profit / signal.Revenue
			out.Margin = &margin
		}
		// Revenue/(revenue+cost) maps break-even to 0.5 and zero-cost
		// revenue to 1, retaining loss information without unstable division
		// by a near-zero margin. Use sums, not a mean of per-request margins.
		cost = signal.Revenue / (signal.Revenue + theoreticalCost)
		out.EconomicsSource = "usage"
		if profit < 0 {
			degraded = true
			out.Reasons = append(out.Reasons, "historical_loss")
		}
	} else if item.account.IsOpenAIApiKey() && out.Rate != nil {
		cost = 1 / (1 + *out.Rate)
		out.EconomicsSource = "rate"
	} else {
		unknown = true
		out.EconomicsSource = "unknown"
		out.Reasons = append(out.Reasons, "profit_insufficient")
	}
	// Realtime failures are an additional penalty, never inferred from zero-cost logs.
	if item.errorRate > 0.2 {
		degraded = true
		out.Reasons = append(out.Reasons, "recent_errors")
	}
	w := c.weights()
	sum := w[0] + w[1] + w[2] + w[3]
	out.Score = 100 * (w[0]*quality + w[1]*latency + w[2]*load + w[3]*cost) / sum * (1 - clamp01(item.errorRate))
	if degraded {
		out.Tier = "degraded"
	} else if unknown {
		out.Tier = "insufficient"
	}
	return out
}

func (s *defaultOpenAIAccountScheduler) applyPriorityScheduling(req OpenAIAccountScheduleRequest, plan *openAIAccountLoadPlan) {
	c := s.service.prioritySchedulingRuntimeConfig()
	if req.Platform != PlatformOpenAI || req.RequiredImageCapability != "" || !req.UseUpstreamTokenCost || !c.applies(req.GroupID, req.RequestedModel) {
		return
	}
	signals, ready := s.service.prioritySignals(req, c, plan.candidates)
	snapshot := &PrioritySchedulingSnapshot{At: time.Now(), Model: req.RequestedModel, GroupID: req.GroupID, Mode: c.Mode, HistoryReady: ready, Candidates: []PrioritySchedulingScore{}}
	if ready {
		for i := range plan.candidates {
			item := &plan.candidates[i]
			score := scorePriorityCandidate(c, *item, signals[item.account.ID], snapshot.At)
			// Disjoint score bands guarantee that cheaper degraded accounts cannot
			// overtake accounts meeting the experience targets, even with custom weights.
			offset := 0.0
			switch score.Tier {
			case "eligible":
				offset = 400
			case "insufficient":
				offset = 200
			}
			item.score = offset + score.Score
			snapshot.Candidates = append(snapshot.Candidates, score)
		}
		plan.priorityScheduling = true
		plan.includeOverflowFallback = true
		slices.SortStableFunc(snapshot.Candidates, func(a, b PrioritySchedulingScore) int {
			tier := func(t string) int {
				if t == "eligible" {
					return 2
				}
				if t == "insufficient" {
					return 1
				}
				return 0
			}
			if tier(a.Tier) != tier(b.Tier) {
				return tier(b.Tier) - tier(a.Tier)
			}
			if a.Priority != b.Priority {
				if a.Priority < b.Priority {
					return -1
				}
				return 1
			}
			if a.Score > b.Score {
				return -1
			}
			if a.Score < b.Score {
				return 1
			}
			return 0
		})
		if len(snapshot.Candidates) > 100 {
			snapshot.Candidates = snapshot.Candidates[:100]
		}
	}
	state := &s.service.priorityScheduling
	state.mu.Lock()
	state.latest = snapshot
	state.mu.Unlock()
}
