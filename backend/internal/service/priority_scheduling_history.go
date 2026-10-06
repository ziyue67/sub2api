package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

const (
	priorityHistoryTTL         = 30 * time.Second
	priorityHistoryMaxAge      = 2 * time.Minute
	priorityHistoryRetry       = 5 * time.Second
	priorityHistoryMaxScopes   = 32
	priorityHistoryMaxAccounts = 2000
)

type prioritySignalEntry struct {
	signals                                map[int64]PrioritySchedulingSignal
	accounts                               map[int64]time.Time
	expires, observed, lastUsed, attempted time.Time
	refreshing                             bool
	failure                                string
}

type prioritySchedulingState struct {
	mu          sync.Mutex
	entries     map[string]*prioritySignalEntry
	active      int
	latestInput *prioritySnapshotInput
}

type priorityHistoryResult struct {
	signals    map[int64]PrioritySchedulingSignal
	status     string
	observed   *time.Time
	failure    string
	refreshing bool
}

func (h priorityHistoryResult) ready() bool { return h.status == "ready" || h.status == "stale" }
func (h priorityHistoryResult) accountStatus(id int64) string {
	if h.status == "limited" || h.status == "unavailable" {
		return h.status
	}
	if _, ok := h.signals[id]; ok {
		if h.status == "stale" || h.failure != "" || (h.observed != nil && time.Since(*h.observed) > priorityHistoryTTL) {
			return "stale"
		}
		return "ready"
	}
	if h.failure != "" {
		return "error"
	}
	return "loading"
}

func (s *OpenAIGatewayService) prioritySignals(req OpenAIAccountScheduleRequest, c PrioritySchedulingConfig, accounts []openAIAccountCandidateScore) (map[int64]PrioritySchedulingSignal, bool) {
	h := s.priorityHistory(req, c, accounts)
	return h.signals, h.ready()
}

// Cache by model/group/window, with a bounded union of recently seen accounts.
// Candidate churn must not erase evidence for peers. Never query on the request
// goroutine; two workers and bounded scopes also apply to dashboard refreshes.
func (s *OpenAIGatewayService) priorityHistory(req OpenAIAccountScheduleRequest, c PrioritySchedulingConfig, accounts []openAIAccountCandidateScore) priorityHistoryResult {
	reader, ok := s.usageLogRepo.(PrioritySchedulingSignalReader)
	if !ok {
		return priorityHistoryResult{status: "unavailable"}
	}
	if len(accounts) == 0 || len(accounts) > priorityHistoryMaxAccounts || len(req.RequestedModel) > 200 {
		return priorityHistoryResult{status: "limited"}
	}
	wanted := make(map[int64]struct{}, len(accounts))
	for _, a := range accounts {
		if a.account != nil {
			wanted[a.account.ID] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return priorityHistoryResult{status: "unavailable"}
	}
	key := priorityHistoryKey(req, c)
	state := &s.priorityScheduling
	state.mu.Lock()
	defer state.mu.Unlock()
	now := time.Now()
	if state.entries == nil {
		state.entries = make(map[string]*prioritySignalEntry)
	}
	entry := state.entries[key]
	if entry == nil {
		if len(state.entries) >= priorityHistoryMaxScopes {
			oldestKey := ""
			var oldest time.Time
			for k, e := range state.entries {
				if !e.refreshing && (oldestKey == "" || e.lastUsed.Before(oldest)) {
					oldestKey, oldest = k, e.lastUsed
				}
			}
			if oldestKey == "" {
				return priorityHistoryResult{status: "limited"}
			}
			delete(state.entries, oldestKey)
		}
		entry = &prioritySignalEntry{accounts: make(map[int64]time.Time)}
		state.entries[key] = entry
	}
	entry.lastUsed = now
	// Prefer current candidates, then retain recently seen peers up to the bound.
	for id, seen := range entry.accounts {
		if _, current := wanted[id]; !current && now.Sub(seen) > priorityHistoryMaxAge {
			delete(entry.accounts, id)
			delete(entry.signals, id)
		}
	}
	for id := range wanted {
		entry.accounts[id] = now
	}
	if len(entry.accounts) > priorityHistoryMaxAccounts {
		old := make([]int64, 0, len(entry.accounts)-len(wanted))
		for id := range entry.accounts {
			if _, current := wanted[id]; !current {
				old = append(old, id)
			}
		}
		slices.SortFunc(old, func(a, b int64) int { return entry.accounts[a].Compare(entry.accounts[b]) })
		for _, id := range old {
			if len(entry.accounts) <= priorityHistoryMaxAccounts {
				break
			}
			delete(entry.accounts, id)
			delete(entry.signals, id)
		}
	}
	missing := false
	for id := range wanted {
		if _, exists := entry.signals[id]; !exists {
			missing = true
			break
		}
	}
	due := now.After(entry.expires) || (missing && entry.failure == "" && now.Sub(entry.attempted) >= time.Second)
	if !entry.refreshing && due && state.active < 2 {
		ids := make([]int64, 0, len(entry.accounts))
		for id := range entry.accounts {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		// Copy the group value before the asynchronous reader outlives its caller.
		var group *int64
		if req.GroupID != nil {
			value := *req.GroupID
			group = &value
		}
		query := PrioritySchedulingQuery{AccountIDs: ids, Model: req.RequestedModel, GroupID: group, UsageSince: now.Add(-time.Duration(c.WindowMinutes) * time.Minute), QualitySince: now.Add(-time.Duration(c.QualityMaxAgeHours) * time.Hour)}
		entry.refreshing = true
		entry.attempted = now
		state.active++
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			signals, err := reader.ReadPrioritySchedulingSignals(ctx, query)
			failure := ""
			if err != nil {
				failure = "query_failed"
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					failure = "timeout"
				}
			}
			if failure != "" {
				var sqlError interface{ SQLState() string }
				sqlState := ""
				if errors.As(err, &sqlError) {
					sqlState = sqlError.SQLState()
				}
				slog.Warn("priority_history_refresh_failed", "reason", failure, "sql_state", sqlState, "accounts", len(ids), "group_id", derefGroupID(query.GroupID), "model", query.Model)
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			state.active--
			entry.refreshing = false
			if err != nil {
				// Keep recent evidence, including known risk, through transient failures.
				entry.failure = failure
				entry.expires = time.Now().Add(priorityHistoryRetry)
				return
			}
			entry.signals = make(map[int64]PrioritySchedulingSignal, len(ids))
			for _, id := range ids {
				if _, tracked := entry.accounts[id]; tracked {
					entry.signals[id] = signals[id]
				}
			}
			entry.failure = ""
			entry.observed = time.Now()
			entry.expires = entry.observed.Add(priorityHistoryTTL)
		}()
	}
	h := priorityHistoryResult{signals: make(map[int64]PrioritySchedulingSignal, len(wanted)), status: "loading", failure: entry.failure, refreshing: entry.refreshing}
	if !entry.observed.IsZero() && now.Sub(entry.observed) <= priorityHistoryMaxAge {
		observed := entry.observed
		h.observed = &observed
		for id := range wanted {
			if signal, exists := entry.signals[id]; exists {
				h.signals[id] = signal
			}
		}
	}
	switch {
	case len(h.signals) == len(wanted):
		h.status = "ready"
		if now.Sub(entry.observed) > priorityHistoryTTL || entry.failure != "" {
			h.status = "stale"
		}
	case len(h.signals) > 0:
		h.status = "partial"
	case entry.failure != "":
		h.status = "error"
	}
	return h
}

// Optional sticky balancing must not schedule SQL or consume refresh workers.
// It may consult recent evidence already loaded by freely routed requests.
func (s *OpenAIGatewayService) cachedPriorityHistory(req OpenAIAccountScheduleRequest, c PrioritySchedulingConfig, accounts []openAIAccountCandidateScore) priorityHistoryResult {
	key := priorityHistoryKey(req, c)
	state := &s.priorityScheduling
	state.mu.Lock()
	defer state.mu.Unlock()
	h := priorityHistoryResult{signals: make(map[int64]PrioritySchedulingSignal)}
	e := state.entries[key]
	if e == nil || e.observed.IsZero() || time.Since(e.observed) > priorityHistoryMaxAge {
		return h
	}
	for _, item := range accounts {
		if signal, exists := e.signals[item.account.ID]; exists {
			h.signals[item.account.ID] = signal
		}
	}
	return h
}

func priorityHistoryKey(req OpenAIAccountScheduleRequest, c PrioritySchedulingConfig) string {
	return fmt.Sprintf("%q/%d/%t/%d/%d", req.RequestedModel, derefGroupID(req.GroupID), req.GroupID != nil, c.WindowMinutes, c.QualityMaxAgeHours)
}
