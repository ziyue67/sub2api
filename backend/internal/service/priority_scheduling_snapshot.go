package service

import (
	"cmp"
	"slices"
	"time"
)

// Store only scoring inputs, never credentials, request bodies or session IDs.
// These owned copies cannot be mutated by a later account/load cache update.
type prioritySnapshotInput struct {
	limited    bool
	at         time.Time
	request    OpenAIAccountScheduleRequest
	config     PrioritySchedulingConfig
	candidates []openAIAccountCandidateScore
	quota      map[int64]float64
}

func newPrioritySnapshotInput(req OpenAIAccountScheduleRequest, c PrioritySchedulingConfig, pool []openAIAccountCandidateScore, at time.Time) *prioritySnapshotInput {
	input := &prioritySnapshotInput{limited: len(pool) > priorityHistoryMaxAccounts, at: at, request: OpenAIAccountScheduleRequest{RequestedModel: req.RequestedModel}, config: c, quota: make(map[int64]float64)}
	if req.GroupID != nil {
		id := *req.GroupID
		input.request.GroupID = &id
	}
	for _, item := range pool[:min(len(pool), priorityHistoryMaxAccounts)] {
		a := item.account
		factor := a.EffectiveLoadFactor()
		projection := &Account{ID: a.ID, Name: a.Name, Platform: a.Platform, Type: a.Type, Priority: a.Priority, Concurrency: a.Concurrency, LoadFactor: &factor, Extra: map[string]any{AccountCostMultiplierExtraKey: a.CostMultiplier()}}
		// Retain binding IDs without retaining mutable group objects.
		projection.GroupIDs = slices.Clone(a.GroupIDs)
		for _, binding := range a.AccountGroups {
			projection.GroupIDs = append(projection.GroupIDs, binding.GroupID)
		}
		item.account = projection
		if item.loadInfo != nil {
			load := *item.loadInfo
			item.loadInfo = &load
		}
		input.candidates = append(input.candidates, item)
		input.quota[a.ID] = openAIQuotaHeadroomFactor(a, at)
	}
	return input
}

// Reading the dashboard refreshes history asynchronously and re-evaluates the
// last observed pool. At still denotes the selection/load observation; a new
// EvaluatedAt must never be presented as a new live capacity measurement.
func (s *OpenAIGatewayService) PrioritySchedulingSnapshot() *PrioritySchedulingSnapshot {
	if s == nil {
		return nil
	}
	state := &s.priorityScheduling
	state.mu.Lock()
	input := state.latestInput
	state.mu.Unlock()
	if input == nil {
		return nil
	}
	h := priorityHistoryResult{status: "limited"}
	if !input.limited {
		h = s.priorityHistory(input.request, input.config, input.candidates)
	}
	now := time.Now()
	var group *int64
	if input.request.GroupID != nil {
		id := *input.request.GroupID
		group = &id
	}
	snapshot := &PrioritySchedulingSnapshot{At: input.at, EvaluatedAt: now, Model: input.request.RequestedModel, GroupID: group, Mode: input.config.Mode, SelectionPolicy: "capacity_first", HistoryReady: h.ready(), HistoryStatus: h.status, HistoryObservedAt: h.observed, HistoryError: h.failure, HistoryRefreshing: h.refreshing, Candidates: make([]PrioritySchedulingScore, 0, len(input.candidates))}
	if input.config.OAuthQuotaPriority {
		snapshot.SelectionPolicy = "oauth_quota_priority"
	}
	for _, item := range input.candidates {
		// Roles describe the observed selection, not a new decision using stale load.
		role := ""
		if item.priorityOAuthSpare > 0 {
			role = "preferred"
		}
		if item.priorityAPIStandby {
			role = "standby"
		}
		score := applyPriorityCandidate(input.config, &item, h.signals[item.account.ID], now)
		score.OAuthQuotaRole = role
		score.ExplorationEligible = item.priorityExploration
		score.BoundGroups = priorityAccountGroupCount(item.account)
		score.CapacityBand = priorityCapacityBand(item)
		score.SelectionWeight = prioritySelectionWeightWithQuota(item, input.quota[item.account.ID])
		score.HistoryStatus = h.accountStatus(item.account.ID)
		snapshot.Candidates = append(snapshot.Candidates, score)
	}
	slices.SortStableFunc(snapshot.Candidates, comparePrioritySnapshotScores)
	snapshot.Candidates = snapshot.Candidates[:min(len(snapshot.Candidates), 100)]
	return snapshot
}

func comparePrioritySnapshotScores(a, b PrioritySchedulingScore) int {
	risk := func(v PrioritySchedulingScore) bool {
		return slices.Contains(v.Reasons, "quality_below_target") || slices.Contains(v.Reasons, "recent_errors") || slices.Contains(v.Reasons, "historical_loss")
	}
	if risk(a) != risk(b) {
		if risk(a) {
			return 1
		}
		return -1
	}
	if v := cmp.Compare(a.CapacityBand, b.CapacityBand); v != 0 {
		return v
	}
	tier := func(t string) int {
		switch t {
		case "eligible":
			return 2
		case "insufficient":
			return 1
		default:
			return 0
		}
	}
	if v := cmp.Compare(tier(b.Tier), tier(a.Tier)); v != 0 {
		return v
	}
	if v := cmp.Compare(a.Priority, b.Priority); v != 0 {
		return v
	}
	if v := cmp.Compare(b.SelectionWeight, a.SelectionWeight); v != 0 {
		return v
	}
	return cmp.Compare(a.AccountID, b.AccountID)
}
