package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	channelCandyMaxProbes             = 64
	ChannelMonitorV2CandyHistoryLimit = 100
	ChannelMonitorV2CandyRetention    = 24 * time.Hour
)

type ChannelMonitorV2CandyProbe struct {
	GroupID         int64  `json:"group_id"`
	Enabled         bool   `json:"enabled"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	IntervalMinutes int    `json:"interval_minutes"`
}

func (p ChannelMonitorV2CandyProbe) key() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("candy-v1:%s:%s:%d", p.Model, p.ReasoningEffort, p.IntervalMinutes))))
}

type ChannelMonitorV2CandyResult struct {
	ID            int64     `json:"-"`
	GroupID       int64     `json:"-"`
	ConfigKey     string    `json:"-"`
	CheckedAt     time.Time `json:"checked_at"`
	Verdict       string    `json:"verdict"`
	LatencyMs     int64     `json:"latency_ms"`
	AnswerPreview string    `json:"answer_preview,omitempty"`
	Reason        string    `json:"reason,omitempty"`
}

type ChannelMonitorV2CandyHistory struct {
	Model           string                        `json:"model"`
	ReasoningEffort string                        `json:"reasoning_effort"`
	IntervalMinutes int                           `json:"interval_minutes"`
	Results         []ChannelMonitorV2CandyResult `json:"results"`
}

type ChannelMonitorV2CandyRepository interface {
	ClaimCandyProbe(context.Context, ChannelMonitorV2CandyProbe, string, time.Time, int) (int64, error)
	FinishCandyProbe(context.Context, ChannelMonitorV2CandyResult) error
	CandyHistory(context.Context, map[int64]string, time.Time) ([]ChannelMonitorV2CandyResult, error)
	PruneCandyHistory(context.Context, time.Time) error
}

type ChannelMonitorV2CandyService struct {
	config   ChannelMonitorV2Repository
	repo     ChannelMonitorV2CandyRepository
	groups   *PelicanGroupTestService
	settings channelMonitorRuntimeReader
	tick     sync.Mutex
	now      func() time.Time
}

func newChannelMonitorV2CandyService(repo ChannelMonitorV2Repository, groups *PelicanGroupTestService, settings channelMonitorRuntimeReader) *ChannelMonitorV2CandyService {
	store, ok := repo.(ChannelMonitorV2CandyRepository)
	if !ok || groups == nil {
		return nil
	}
	return &ChannelMonitorV2CandyService{config: repo, repo: store, groups: groups, settings: settings, now: time.Now}
}

func normalizeChannelMonitorV2CandyProbes(probes []ChannelMonitorV2CandyProbe) error {
	if len(probes) > channelCandyMaxProbes {
		return fmt.Errorf("%w: at most %d candy probes", ErrChannelMonitorV2InvalidConfig, channelCandyMaxProbes)
	}
	seen := map[int64]bool{}
	for i := range probes {
		p := &probes[i]
		if p.GroupID <= 0 || seen[p.GroupID] {
			return fmt.Errorf("%w: invalid or duplicate candy group", ErrChannelMonitorV2InvalidConfig)
		}
		seen[p.GroupID] = true
		p.Model = strings.TrimSpace(p.Model)
		if p.Model == "" || len(p.Model) > 100 {
			return fmt.Errorf("%w: candy model is required (maximum 100 bytes)", ErrChannelMonitorV2InvalidConfig)
		}
		if p.ReasoningEffort == "" {
			p.ReasoningEffort = "medium"
		}
		p.ReasoningEffort = normalizePelicanReasoningEffort(p.ReasoningEffort)
		if p.ReasoningEffort == "" {
			return fmt.Errorf("%w: invalid candy reasoning effort", ErrChannelMonitorV2InvalidConfig)
		}
		if p.IntervalMinutes == 0 {
			p.IntervalMinutes = 1
		}
		if p.IntervalMinutes < 1 || p.IntervalMinutes > 1440 {
			return fmt.Errorf("%w: candy interval must be 1-1440 minutes", ErrChannelMonitorV2InvalidConfig)
		}
	}
	return nil
}

func (s *ChannelMonitorV2CandyService) validateGroups(ctx context.Context, probes []ChannelMonitorV2CandyProbe) error {
	for _, p := range probes {
		group, err := s.groups.groups.GetByID(ctx, p.GroupID)
		if err != nil || group == nil {
			return fmt.Errorf("%w: candy group %d is unavailable", ErrChannelMonitorV2InvalidConfig, p.GroupID)
		}
	}
	return nil
}

// RunDue uses the same group scheduler, model mapping, account concurrency and
// failover policy as /admin/pelican-tests. A wrong answer is retained and never
// retried on another account to obtain a passing sample. It does not bill a user,
// publish a showcase item, apply answer-based account actions, or enter passive
// usage totals. Authentication/rate-limit health handling remains in the shared
// account-test and gateway paths.
func (s *ChannelMonitorV2CandyService) RunDue(ctx context.Context, now time.Time) {
	if s == nil || !s.tick.TryLock() {
		return
	}
	defer s.tick.Unlock()
	if err := s.repo.PruneCandyHistory(ctx, now); err != nil {
		logger.LegacyPrintf("service.channel_monitor_v2", "candy retention failed: %v", err)
	}
	if s.settings == nil || !s.settings.GetChannelMonitorRuntime(ctx).V2Active() {
		return
	}
	cfg, err := s.config.GetConfig(ctx)
	if err != nil || cfg == nil || !cfg.Enabled {
		return
	}
	jobs := make(chan ChannelMonitorV2CandyProbe)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for probe := range jobs {
				s.runOne(ctx, probe, cfg, now)
			}
		}()
	}
	for _, probe := range cfg.CandyProbes {
		if !probe.Enabled || (len(cfg.GroupIDs) > 0 && !slices.Contains(cfg.GroupIDs, probe.GroupID)) {
			continue
		}
		select {
		case jobs <- probe:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *ChannelMonitorV2CandyService) runOne(ctx context.Context, probe ChannelMonitorV2CandyProbe, cfg *ChannelMonitorV2Config, tick time.Time) {
	if ctx.Err() != nil || !s.settings.GetChannelMonitorRuntime(ctx).V2Active() {
		return
	}
	group, err := s.groups.groups.GetByID(ctx, probe.GroupID)
	if err != nil || group == nil || group.Status != StatusActive {
		return
	}
	platformEnabled := false
	for _, p := range cfg.Platforms {
		if p.Enabled && (p.Platform == group.Platform || group.Platform == PlatformComposite) {
			platformEnabled = true
			break
		}
	}
	if !platformEnabled {
		return
	}
	slot := tick.UTC().Truncate(time.Duration(probe.IntervalMinutes) * time.Minute)
	id, err := s.repo.ClaimCandyProbe(ctx, probe, probe.key(), slot, cfg.Version)
	if err != nil {
		logger.LegacyPrintf("service.channel_monitor_v2", "candy claim group=%d failed: %v", probe.GroupID, err)
		return
	}
	if id == 0 {
		return
	}
	result := ChannelMonitorV2CandyResult{ID: id, GroupID: probe.GroupID, ConfigKey: probe.key(), CheckedAt: s.now(), Verdict: "error", Reason: "interrupted"}
	defer func() {
		if recover() != nil {
			result.Verdict = "error"
			result.Reason = "probe_failed"
		}
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.repo.FinishCandyProbe(saveCtx, result); err != nil {
			logger.LegacyPrintf("service.channel_monitor_v2", "candy save group=%d failed: %v", probe.GroupID, err)
		}
	}()
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	plan := &PelicanGroupTestPlan{GroupID: probe.GroupID, ModelID: probe.Model, PelicanConfig: &PelicanTestConfig{QuestionKind: "candy", Prompt: CandyPrompt, ReasoningEffort: probe.ReasoningEffort, ParallelCount: 1, ModelID: probe.Model}}
	sample := s.groups.runSample(runCtx, plan, group)
	result.LatencyMs = s.now().Sub(result.CheckedAt).Milliseconds()
	if runCtx.Err() != nil {
		result.Reason = "timeout"
		return
	}
	if sample == nil {
		result.Reason = "probe_failed"
		return
	}
	answer := strings.TrimSpace(sample.ResponseText)
	preview := []rune(answer)
	result.AnswerPreview = string(preview[:min(len(preview), 512)])
	if sample.Status != "success" && !strings.HasPrefix(sample.ErrorMessage, "answer_mismatch:") {
		result.Reason = "probe_failed"
		return
	}
	if answer == "" {
		result.Reason = "empty_answer"
		return
	}
	result.Reason = ""
	result.Verdict = "incorrect"
	if CandyAnswerCorrect(answer) {
		result.Verdict = "correct"
	}
}

func (s *ChannelMonitorV2CandyService) attachHistory(ctx context.Context, matrix *ChannelMonitorV2Matrix, cfg *ChannelMonitorV2Config, admin bool) error {
	probes := map[int64]ChannelMonitorV2CandyProbe{}
	for _, p := range cfg.CandyProbes {
		if p.Enabled {
			probes[p.GroupID] = p
		}
	}
	configs := map[int64]string{}
	for _, row := range matrix.Items {
		if row.GroupID != nil {
			if probe, ok := probes[*row.GroupID]; ok {
				configs[*row.GroupID] = probe.key()
			}
		}
	}
	if len(configs) == 0 {
		return nil
	}
	// Only groups already admitted by the matrix's server-side scope are read.
	history, err := s.repo.CandyHistory(ctx, configs, s.now().Add(-ChannelMonitorV2CandyRetention))
	if err != nil {
		return err
	}
	byGroup := map[int64][]ChannelMonitorV2CandyResult{}
	for _, r := range history {
		if p, ok := probes[r.GroupID]; !ok || p.key() != r.ConfigKey {
			continue
		}
		if !admin {
			r.AnswerPreview = ""
			r.Reason = ""
		}
		byGroup[r.GroupID] = append(byGroup[r.GroupID], r)
	}
	for i := range matrix.Items {
		row := &matrix.Items[i]
		if row.GroupID == nil {
			continue
		}
		p, ok := probes[*row.GroupID]
		if !ok {
			continue
		}
		results := byGroup[p.GroupID]
		if results == nil {
			results = []ChannelMonitorV2CandyResult{}
		}
		row.Candy = &ChannelMonitorV2CandyHistory{Model: p.Model, ReasoningEffort: p.ReasoningEffort, IntervalMinutes: p.IntervalMinutes, Results: results}
	}
	return nil
}
