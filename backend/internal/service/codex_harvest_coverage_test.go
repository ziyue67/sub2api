package service

import (
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestHarvestCoverageFifteenReadySixtyMissing(t *testing.T) {
	var tracker harvestCoverageTracker
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for seconds := 0; seconds <= 75*60; seconds += 5 {
		tracker.observe(start.Add(time.Duration(seconds)*time.Second), "scope", map[int64]time.Time{1: start.Add(15 * time.Minute)})
	}
	got := tracker.snapshot()
	require.Equal(t, 1, got.AccountCount)
	require.Equal(t, 900.0, got.AverageReadySeconds)
	require.Equal(t, 3600.0, got.AverageMissingSeconds)
	require.InDelta(t, 20, *got.AveragePercent, 0.001)
	require.InDelta(t, 20, *got.Accounts[1].Percent, 0.001)
	require.Equal(t, 900.0, got.Accounts[1].ReadySeconds)
	require.Equal(t, 3600.0, got.Accounts[1].MissingSeconds)
}

func TestHarvestCoverageConcurrentSnapshot(t *testing.T) {
	var tracker harvestCoverageTracker
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				tracker.observe(time.Now(), "a", map[int64]time.Time{1: {}})
				tracker.snapshot()
			}
		}()
	}
	wg.Wait()
}

func TestHarvestCoverageExpiryGapsAndReset(t *testing.T) {
	var tracker harvestCoverageTracker
	start := time.Now()
	tracker.observe(start, "a", map[int64]time.Time{1: start.Add(2 * time.Second)})
	require.Nil(t, tracker.snapshot().AveragePercent)
	tracker.observe(start.Add(5*time.Second), "a", map[int64]time.Time{1: {}})
	got := tracker.snapshot()
	require.Equal(t, 2.0, got.AverageReadySeconds)
	require.Equal(t, 3.0, got.AverageMissingSeconds)
	// A minute without successful observations is unknown, not missing time.
	tracker.observe(start.Add(65*time.Second), "a", map[int64]time.Time{1: {}})
	require.Equal(t, 3.0, tracker.snapshot().AverageMissingSeconds)
	tracker.observe(start.Add(70*time.Second), "b", map[int64]time.Time{1: {}})
	require.Nil(t, tracker.snapshot().AveragePercent)
	tracker.observe(start.Add(75*time.Second), "b", map[int64]time.Time{})
	require.Zero(t, tracker.snapshot().AccountCount)
}

func TestHarvestCoverageEqualAccountWeightAndReentry(t *testing.T) {
	var tracker harvestCoverageTracker
	start := time.Now()
	tracker.observe(start, "a", map[int64]time.Time{1: start.Add(time.Hour)})
	tracker.observe(start.Add(5*time.Second), "a", map[int64]time.Time{1: start.Add(time.Hour), 2: {}})
	tracker.observe(start.Add(10*time.Second), "a", map[int64]time.Time{1: start.Add(time.Hour), 2: {}})
	got := tracker.snapshot()
	require.InDelta(t, 50, *got.AveragePercent, 0.001) // Not duration-weighted 66.7%.
	require.Equal(t, 5.0, got.AverageReadySeconds)
	require.Equal(t, 2.5, got.AverageMissingSeconds)
	tracker.observe(start.Add(15*time.Second), "a", map[int64]time.Time{2: {}})
	tracker.observe(start.Add(20*time.Second), "a", map[int64]time.Time{1: {}, 2: {}})
	require.Equal(t, 1, tracker.snapshot().AccountCount) // Reentry has no history yet.
}

func TestHarvestCoverageInterruptedObservation(t *testing.T) {
	var tracker harvestCoverageTracker
	start := time.Now()
	tracker.observe(start, "a", map[int64]time.Time{1: {}})
	tracker.observe(start.Add(5*time.Second), "a", map[int64]time.Time{1: {}})
	tracker.interrupt()
	tracker.observe(start.Add(10*time.Second), "a", map[int64]time.Time{1: {}})
	require.Equal(t, 5.0, tracker.snapshot().AverageMissingSeconds)
}

func TestHarvestCoverageWorkerUsesValidTicketsAndScope(t *testing.T) {
	gateway, _, seed := unifiedAstraFixture(t)
	a := ticketTestAccount(1)
	a.Status, a.Schedulable = StatusActive, true
	b := ticketTestAccount(2)
	b.Status, b.Schedulable = StatusActive, true
	b.Extra = map[string]any{OpenAICodexSkipHarvestExtraKey: true}
	repo := &poolSchedulingTestRepo{astraSchedulingRepo: astraSchedulingRepo{accounts: map[int64]*Account{1: a, 2: b}}}
	gateway.accountRepo = repo
	gateway.codexHarvest = NewCodexHarvestService(nil, nil, gateway.cfg)
	gateway.codexHarvest.loadedUntil = time.Now().Add(time.Hour)
	ticket := *seed
	ticket.Identity = ticketIdentity(a)
	gateway.openaiCodexTickets.Store(openAICodexTicketKey(1, "gpt-6-astra"), &ticket)
	gateway.observeHarvestCoverage(t.Context())
	require.Len(t, gateway.codexHarvest.coverage.accounts, 1)
	require.True(t, gateway.codexHarvest.coverage.accounts[1].readyUntil.After(time.Now()))
	ticket.ProbeVerified = false
	gateway.openaiCodexTickets.Store(openAICodexTicketKey(1, "gpt-6-astra"), &ticket)
	gateway.observeHarvestCoverage(t.Context())
	require.True(t, gateway.codexHarvest.coverage.accounts[1].readyUntil.IsZero())
}
