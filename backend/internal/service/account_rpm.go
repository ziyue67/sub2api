package service

import (
	"context"
	"errors"
	"time"
)

const accountRPMWarningRatio = 0.8

var errRPMCacheNotConfigured = errors.New("RPM counter cache is not configured")

// AccountRPMState is shared by provider scheduling and admission checks.
type AccountRPMState struct {
	Current int
	Limit   int
	ResetAt time.Time
	Enabled bool
}

func (s AccountRPMState) Utilization() float64 {
	if !s.Enabled || s.Limit <= 0 {
		return 0
	}
	return float64(s.Current) / float64(s.Limit)
}

func (a *Account) SupportsRPMLimit() bool {
	return a != nil && (a.IsAnthropicOAuthOrSetupToken() || a.IsOpenAIOAuth())
}

func accountRPMState(account *Account, count int) AccountRPMState {
	if !account.SupportsRPMLimit() || account.GetBaseRPM() <= 0 {
		return AccountRPMState{}
	}
	return AccountRPMState{
		Current: count, Limit: account.GetBaseRPM(), Enabled: true,
		ResetAt: time.Now().Truncate(time.Minute).Add(time.Minute),
	}
}

type rpmPrefetchContextKeyType struct{}

var rpmPrefetchContextKey = rpmPrefetchContextKeyType{}

func rpmFromPrefetchContext(ctx context.Context, accountID int64) (int, bool) {
	if counts, ok := ctx.Value(rpmPrefetchContextKey).(map[int64]int); ok {
		count, found := counts[accountID]
		return count, found
	}
	return 0, false
}

func accountRPMStateFromContext(ctx context.Context, account *Account) (AccountRPMState, bool) {
	if !account.SupportsRPMLimit() || account.GetBaseRPM() <= 0 {
		return AccountRPMState{}, false
	}
	count, ok := rpmFromPrefetchContext(ctx, account.RPMAccountID())
	return accountRPMState(account, count), ok
}

// Cache failures are returned to the provider adapter. Anthropic keeps its
// existing fail-open policy; strict admission must reject on these errors.
func readAccountRPMState(ctx context.Context, cache RPMCache, account *Account) (AccountRPMState, error) {
	state := accountRPMState(account, 0)
	if !state.Enabled {
		return state, nil
	}
	if prefetched, ok := accountRPMStateFromContext(ctx, account); ok {
		return prefetched, nil
	}
	if cache == nil {
		return state, errRPMCacheNotConfigured
	}
	count, err := cache.GetRPM(ctx, account.RPMAccountID())
	if err != nil {
		return state, err
	}
	state.Current = count
	return state, nil
}

func withAccountRPMPrefetch(ctx context.Context, cache RPMCache, accounts []Account, platform string) (context.Context, error) {
	ids := make([]int64, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform || !account.SupportsRPMLimit() || account.GetBaseRPM() <= 0 {
			continue
		}
		id := account.RPMAccountID()
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
			seen[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return ctx, nil
	}
	if cache == nil {
		return ctx, errRPMCacheNotConfigured
	}
	counts, err := cache.GetRPMBatch(ctx, ids)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, rpmPrefetchContextKey, counts), nil
}
