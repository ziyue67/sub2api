package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPriorityLatencyPenaltyExpiresWithoutNewTraffic(t *testing.T) {
	now := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	cfg := DefaultPrioritySchedulingConfig()
	cfg.RecoveryExplorationPercent = 10
	cfg.TargetTTFTMs = 15000
	passed := true
	signal := PrioritySchedulingSignal{Samples: 7, ProfitSamples: 7, P90TTFTMs: 19172.2, QualityPassed: 68, QualitySamples: 68, LatestQualityPassed: &passed, LatestLatencyAt: &now, LatestBadLatencyAt: &now, Revenue: 1.2, BaseCost: 4.08}
	deadline := now.Add(10 * time.Minute)
	item := priorityCandidate(1, 0.11, 0)
	for _, at := range []time.Time{now, deadline.Add(-time.Nanosecond)} {
		score := applyPriorityCandidate(cfg, &item, signal, at)
		require.Equal(t, "degraded", score.Tier)
		require.Equal(t, deadline, *score.LatencyPenaltyUntil)
		require.False(t, item.priorityExploration)
	}
	for _, at := range []time.Time{deadline, deadline.Add(time.Minute)} {
		score := applyPriorityCandidate(cfg, &item, signal, at)
		require.Equal(t, "insufficient", score.Tier)
		require.True(t, score.LatencyRecovery)
		require.True(t, item.priorityExploration)
		require.Equal(t, deadline, *score.LatencyPenaltyUntil, "re-reading old history must not restart the penalty")
		require.Less(t, item.priorityLatencyFactor, 1.0, "recovery retains latency protection")
	}
	fresh := deadline.Add(time.Minute)
	signal.LatestLatencyAt = &fresh
	signal.LatestBadLatencyAt = &fresh
	score := applyPriorityCandidate(cfg, &item, signal, fresh)
	require.Equal(t, "degraded", score.Tier, "a fresh above-target sample re-arms the penalty")
	require.Equal(t, fresh.Add(10*time.Minute), *score.LatencyPenaltyUntil)
	signal.P90TTFTMs = 14000
	score = applyPriorityCandidate(cfg, &item, signal, fresh)
	require.Equal(t, "eligible", score.Tier, "improved P90 restores the normal pool immediately")
	require.Nil(t, score.LatencyPenaltyUntil)
}

func TestPriorityLatencyRecoverySharesBoundedExploration(t *testing.T) {
	now := time.Now()
	old := now.Add(-11 * time.Minute)
	passed := true
	cfg := DefaultPrioritySchedulingConfig()
	cfg.TargetTTFTMs = 3000
	cfg.RecoveryExplorationPercent = 10
	cfg.Mode = "custom"
	cfg.QualityWeight, cfg.LatencyWeight, cfg.LoadWeight, cfg.CostWeight = 2, 2, 1, 95
	signal := PrioritySchedulingSignal{Samples: 7, ProfitSamples: 7, P90TTFTMs: 5000, QualitySamples: 10, QualityPassed: 10, LatestQualityPassed: &passed, LatestLatencyAt: &old, LatestBadLatencyAt: &old, Revenue: 1, BaseCost: 2}
	healthy, recovering := priorityCandidate(1, 0.17, 0), priorityCandidate(2, 0.11, 0)
	good := signal
	good.P90TTFTMs = 1000
	applyPriorityCandidate(cfg, &healthy, good, now)
	applyPriorityCandidate(cfg, &recovering, signal, now)
	require.True(t, recovering.priorityExploration)
	counts := map[int64]int{}
	for i := 0; i < 5000; i++ {
		order := buildPrioritySelectionOrder([]openAIAccountCandidateScore{healthy, recovering}, OpenAIAccountScheduleRequest{SessionHash: fmt.Sprintf("recovery-%d", i)})
		counts[order[0].account.ID]++
	}
	require.InDelta(t, 500, counts[2], 100, "recovery needs traffic without overtaking the healthy pool: %v", counts)
	require.Greater(t, counts[1], 4300)
	// Recovery continues to obey known risk and latest quality evidence.
	for _, tc := range []struct {
		name   string
		change func(*PrioritySchedulingSignal, *openAIAccountCandidateScore)
	}{
		{"poor_quality", func(s *PrioritySchedulingSignal, _ *openAIAccountCandidateScore) { s.QualityPassed = 5 }},
		{"unknown_quality", func(s *PrioritySchedulingSignal, _ *openAIAccountCandidateScore) { s.QualitySamples = 0 }},
		{"latest_quality_failed", func(s *PrioritySchedulingSignal, _ *openAIAccountCandidateScore) {
			v := false
			s.LatestQualityPassed = &v
		}},
		{"latest_quality_missing", func(s *PrioritySchedulingSignal, _ *openAIAccountCandidateScore) { s.LatestQualityPassed = nil }},
		{"missing_quality_with_insufficient_profit", func(s *PrioritySchedulingSignal, _ *openAIAccountCandidateScore) {
			s.LatestQualityPassed = nil
			s.ProfitSamples = 0
		}},
		{"loss", func(s *PrioritySchedulingSignal, _ *openAIAccountCandidateScore) { s.Revenue = 0.01 }},
		{"errors", func(_ *PrioritySchedulingSignal, a *openAIAccountCandidateScore) { a.errorRate = 0.8 }},
		{"scoped_errors", func(_ *PrioritySchedulingSignal, a *openAIAccountCandidateScore) { a.priorityErrors.SmoothedRate = 0.8 }},
		{"busy", func(_ *PrioritySchedulingSignal, a *openAIAccountCandidateScore) { a.loadInfo.CurrentConcurrency = 9 }},
		{"unknown_load", func(_ *PrioritySchedulingSignal, a *openAIAccountCandidateScore) { a.loadKnown = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sample := signal
			account := priorityCandidate(2, 0.11, 0)
			tc.change(&sample, &account)
			localConfig := cfg
			if tc.name == "errors" {
				localConfig.ErrorRateWeight = 0
			} // Existing account EWMA contract.
			applyPriorityCandidate(localConfig, &account, sample, now)
			require.False(t, account.priorityExploration)
		})
	}
}

func TestPriorityLatencyPenaltyCompatibilityAndBounds(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	cfg.TargetTTFTMs = 3000
	// Persisted old configurations inherit the default, while explicit zero
	// retains the prior continuous penalty. Missing timestamps fail closed.
	old, err := json.Marshal(cfg)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(old, &body))
	delete(body, "latency_penalty_minutes")
	old, err = json.Marshal(body)
	require.NoError(t, err)
	settings := &SettingService{settingRepo: &prioritySettingStub{value: string(old)}}
	loaded, err := settings.GetPrioritySchedulingConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, 10, loaded.LatencyPenaltyMinutes)
	for _, minutes := range []int{0, 1, 1440} {
		cfg.LatencyPenaltyMinutes = minutes
		require.NoError(t, ValidatePrioritySchedulingConfig(cfg))
	}
	for _, minutes := range []int{-1, 1441} {
		cfg.LatencyPenaltyMinutes = minutes
		require.Error(t, ValidatePrioritySchedulingConfig(cfg))
	}
	now := time.Now()
	oldAt := now.Add(-time.Hour)
	signal := PrioritySchedulingSignal{Samples: 7, P90TTFTMs: 5000, QualitySamples: 10, QualityPassed: 10, LatestLatencyAt: &oldAt, LatestBadLatencyAt: &oldAt}
	cfg.LatencyPenaltyMinutes = 0
	require.Equal(t, "degraded", scorePriorityCandidate(cfg, priorityCandidate(1, 0.11, 0), signal, now).Tier)
	cfg.LatencyPenaltyMinutes = 10
	signal.LatestLatencyAt = nil
	signal.LatestBadLatencyAt = nil
	require.Equal(t, "degraded", scorePriorityCandidate(cfg, priorityCandidate(1, 0.11, 0), signal, now).Tier)
}
