package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPriorityTeamsPaidWindowRecovery(t *testing.T) {
	now := time.Now()
	start, end := now.Add(-time.Hour), now.Add(3*time.Hour)
	c := DefaultPrioritySchedulingConfig()
	c.Teams.Enabled = true
	item := priorityCandidate(1, 100, 10)
	item.account.Type = AccountTypeOAuth
	item.account.Credentials = map[string]any{"plan_type": "team"}
	signal := PrioritySchedulingSignal{Samples: 10, P90TTFTMs: 1000, QualitySamples: 10, QualityPassed: 10, TeamsWindowStart: &start, TeamsWindowEnd: &end, TeamsRevenue: 20}
	score := scorePriorityCandidate(c, item, signal, now)
	require.Equal(t, "eligible", score.Tier, "a new paid account is not degraded because it has yet to recover sunk cost")
	require.Equal(t, "teams_window", score.EconomicsSource)
	require.True(t, score.TeamsRecovery.NeedsRecovery)
	require.Equal(t, 30.0, score.TeamsRecovery.ShortfallCNY)
	require.InDelta(t, 10, score.TeamsRecovery.RequiredRevenuePerHourCNY, 0.01)
	require.Equal(t, -30.0, *score.Profit)
	signal.TeamsRevenue = 50
	require.True(t, scorePriorityCandidate(c, item, signal, now).TeamsRecovery.NeedsRecovery, "break-even is not yet positive profit")
	signal.TeamsRevenue = 50.01
	require.False(t, scorePriorityCandidate(c, item, signal, now).TeamsRecovery.NeedsRecovery)
	c.Teams.CNYPerBillingUnit = 7
	signal.TeamsRevenue = 8
	require.Equal(t, 56.0, scorePriorityCandidate(c, item, signal, now).TeamsRecovery.RevenueCNY)
	signal.QualityPassed = 1
	require.Equal(t, "degraded", scorePriorityCandidate(c, item, signal, now).Tier, "recovery must not override quality")
	require.Nil(t, scorePriorityTeams(c.Teams, signal, end))
	require.Nil(t, scorePriorityTeams(c.Teams, signal, start.Add(-time.Second)))
}
func TestPriorityTeamsWindowAnchors(t *testing.T) {
	c := DefaultPrioritySchedulingConfig().Teams
	c.Enabled = true
	c.WindowSource = "explicit"
	item := priorityCandidate(1, 1, 0)
	item.account.Type = AccountTypeOAuth
	item.account.Credentials = map[string]any{"plan_type": "business"}
	now := time.Now().Truncate(time.Second)
	item.account.Extra = map[string]any{"priority_teams_window_start": now.Format(time.RFC3339)}
	windows := priorityTeamsWindows(c, []openAIAccountCandidateScore{item})
	require.Len(t, windows, 1)
	require.Equal(t, 4*time.Hour, windows[0].End.Sub(*windows[0].Start))
	end := now.Add(4 * time.Hour)
	item.account.ExpiresAt = &end
	c.WindowSource = "expiry"
	windows = priorityTeamsWindows(c, []openAIAccountCandidateScore{item})
	require.Equal(t, now, *windows[0].Start)
	parent := int64(2)
	item.account.ParentAccountID = &parent
	require.Empty(t, priorityTeamsWindows(c, []openAIAccountCandidateScore{item}), "shadow is not a second purchased account")
}
func TestPriorityTeamsRecoveryPrecedesMultiplierAndPriorityWithinTier(t *testing.T) {
	a, b, poor := priorityCandidate(1, 100, 0), priorityCandidate(2, 0.01, 0), priorityCandidate(3, 0, 0)
	a.account.Priority = 100
	b.account.Priority = 1
	poor.account.Priority = 0
	a.score = 410
	b.score = 490
	poor.score = 99
	a.priorityRecovery = true
	a.priorityRecoveryPressure = 10
	poor.priorityRecovery = true
	poor.priorityRecoveryPressure = 1000
	s, ok := newDefaultOpenAIAccountScheduler(&OpenAIGatewayService{}, nil).(*defaultOpenAIAccountScheduler)
	require.True(t, ok)
	got := s.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{}, openAIAccountLoadPlan{priorityScheduling: true, topK: 1, candidates: []openAIAccountCandidateScore{b, poor, a}})
	require.Equal(t, int64(1), got[0].account.ID)
	require.Equal(t, int64(2), got[1].account.ID)
	require.Equal(t, int64(3), got[2].account.ID)
	a.priorityRecovery = false
	got = s.buildOpenAISelectionOrder(OpenAIAccountScheduleRequest{}, openAIAccountLoadPlan{priorityScheduling: true, topK: 1, candidates: []openAIAccountCandidateScore{a, b}})
	require.Equal(t, int64(2), got[0].account.ID, "after recovery resume explicit priority and economic scores")
}
