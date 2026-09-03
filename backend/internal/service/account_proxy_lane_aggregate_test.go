package service

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// aggregateConcurrencyCache is a small in-memory implementation of the two
// reservation namespaces.  It embeds the broad legacy interface so this test
// only has to model the account/lane methods exercised by the composite
// admission path.
type aggregateConcurrencyCache struct {
	ConcurrencyCache

	mu       sync.Mutex
	accounts map[int64]map[string]struct{}
	lanes    map[int64]map[string]struct{}
}

var _ ConcurrencyCache = (*aggregateConcurrencyCache)(nil)
var _ LaneConcurrencyCache = (*aggregateConcurrencyCache)(nil)

func newAggregateConcurrencyCache() *aggregateConcurrencyCache {
	return &aggregateConcurrencyCache{
		accounts: make(map[int64]map[string]struct{}),
		lanes:    make(map[int64]map[string]struct{}),
	}
}

func acquireAggregateSlot(mu *sync.Mutex, slots map[int64]map[string]struct{}, id int64, max int, requestID string) bool {
	mu.Lock()
	defer mu.Unlock()
	if max <= 0 {
		return true
	}
	set := slots[id]
	if set == nil {
		set = make(map[string]struct{})
		slots[id] = set
	}
	if len(set) >= max {
		return false
	}
	set[requestID] = struct{}{}
	return true
}

func releaseAggregateSlot(mu *sync.Mutex, slots map[int64]map[string]struct{}, id int64, requestID string) error {
	mu.Lock()
	defer mu.Unlock()
	if set := slots[id]; set != nil {
		delete(set, requestID)
		if len(set) == 0 {
			delete(slots, id)
		}
	}
	return nil
}

func (c *aggregateConcurrencyCache) AcquireAccountSlot(_ context.Context, accountID int64, max int, requestID string) (bool, error) {
	return acquireAggregateSlot(&c.mu, c.accounts, accountID, max, requestID), nil
}

func (c *aggregateConcurrencyCache) ReleaseAccountSlot(_ context.Context, accountID int64, requestID string) error {
	return releaseAggregateSlot(&c.mu, c.accounts, accountID, requestID)
}

func (c *aggregateConcurrencyCache) AcquireLaneSlot(_ context.Context, laneID int64, max int, requestID string) (bool, error) {
	return acquireAggregateSlot(&c.mu, c.lanes, laneID, max, requestID), nil
}

func (c *aggregateConcurrencyCache) ReleaseLaneSlot(_ context.Context, laneID int64, requestID string) error {
	return releaseAggregateSlot(&c.mu, c.lanes, laneID, requestID)
}

func (c *aggregateConcurrencyCache) GetAccountConcurrency(_ context.Context, accountID int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.accounts[accountID]), nil
}

func (c *aggregateConcurrencyCache) GetLaneConcurrency(_ context.Context, laneID int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lanes[laneID]), nil
}

func (c *aggregateConcurrencyCache) IncrementLaneWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}

func (c *aggregateConcurrencyCache) DecrementLaneWaitCount(context.Context, int64) error {
	return nil
}

func (c *aggregateConcurrencyCache) GetLaneWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}

func (c *aggregateConcurrencyCache) accountCount(accountID int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.accounts[accountID])
}

func (c *aggregateConcurrencyCache) laneCount(laneID int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lanes[laneID])
}

func TestAcquireAccountAndLaneSlotEnforcesAggregateAndLaneCaps(t *testing.T) {
	cache := newAggregateConcurrencyCache()
	svc := NewConcurrencyService(cache)
	require.True(t, svc.LaneConcurrencySupported())
	ctx := context.Background()

	var releases []func()
	for i := 0; i < 10; i++ {
		result, err := svc.AcquireAccountAndLaneSlot(ctx, 75, 20, 7501, 10)
		require.NoError(t, err)
		require.Truef(t, result.Acquired, "lane1 acquisition %d", i+1)
		releases = append(releases, result.ReleaseFunc)
		require.Equal(t, 20, result.AggregateMaxConcurrency)
		require.Equal(t, 10, result.MaxConcurrency)
	}

	// The lane cap rejects the eleventh request even though the account still
	// has aggregate capacity.  The failed lane probe must roll back its account
	// reservation rather than consuming one of the remaining ten total slots.
	result, err := svc.AcquireAccountAndLaneSlot(ctx, 75, 20, 7501, 10)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Equal(t, 10, cache.accountCount(75))
	require.Equal(t, 10, cache.laneCount(7501))

	for i := 0; i < 10; i++ {
		result, err := svc.AcquireAccountAndLaneSlot(ctx, 75, 20, 7502, 10)
		require.NoError(t, err)
		require.Truef(t, result.Acquired, "lane2 acquisition %d", i+1)
		releases = append(releases, result.ReleaseFunc)
	}
	require.Equal(t, 20, cache.accountCount(75))
	require.Equal(t, 10, cache.laneCount(7501))
	require.Equal(t, 10, cache.laneCount(7502))

	// Both lanes are individually at ten and the parent account is at twenty;
	// no twenty-first request may pass.
	result, err = svc.AcquireAccountAndLaneSlot(ctx, 75, 20, 7502, 10)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Equal(t, 20, cache.accountCount(75), "failed composite acquire leaked an account slot")

	// Releasing the composite lease frees one slot in each namespace.  A new
	// request can then use the same lane and the account total returns to 20.
	releases[0]()
	require.Equal(t, 19, cache.accountCount(75))
	require.Equal(t, 9, cache.laneCount(7501))
	result, err = svc.AcquireAccountAndLaneSlot(ctx, 75, 20, 7501, 10)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, 20, cache.accountCount(75))
	require.Equal(t, 10, cache.laneCount(7501))
	releases = append(releases, result.ReleaseFunc)
	for _, release := range releases[1:] {
		release()
	}
	require.Equal(t, 0, cache.accountCount(75))
	require.Equal(t, 0, cache.laneCount(7501))
	require.Equal(t, 0, cache.laneCount(7502))
}

func TestAcquireAccountAndLaneSlotSupportsIndependentUnlimitedSide(t *testing.T) {
	ctx := context.Background()

	// Aggregate zero means unlimited at the account level; the lane cap still
	// applies independently.
	cache := newAggregateConcurrencyCache()
	svc := NewConcurrencyService(cache)
	leases := make([]func(), 0, 2)
	for i := 0; i < 2; i++ {
		result, err := svc.AcquireAccountAndLaneSlot(ctx, 76, 0, 7601, 2)
		require.NoError(t, err)
		require.True(t, result.Acquired)
		leases = append(leases, result.ReleaseFunc)
	}
	result, err := svc.AcquireAccountAndLaneSlot(ctx, 76, 0, 7601, 2)
	require.NoError(t, err)
	require.False(t, result.Acquired, "lane cap must remain active when aggregate is unlimited")
	require.Equal(t, 0, cache.accountCount(76))
	require.Equal(t, 2, cache.laneCount(7601))
	for _, release := range leases {
		release()
	}
	require.Equal(t, 0, cache.laneCount(7601))

	// Lane zero means unlimited for that egress; the parent account cap remains
	// authoritative and blocks the third request.
	leases = make([]func(), 0, 2)
	for i := 0; i < 2; i++ {
		result, err := svc.AcquireAccountAndLaneSlot(ctx, 77, 2, 7701, 0)
		require.NoError(t, err)
		require.True(t, result.Acquired)
		leases = append(leases, result.ReleaseFunc)
	}
	result, err = svc.AcquireAccountAndLaneSlot(ctx, 77, 2, 7701, 0)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Equal(t, 2, cache.accountCount(77))
	for _, release := range leases {
		release()
	}
	require.Equal(t, 0, cache.accountCount(77))
}

func TestAcquireAccountProxyLaneSlotUsesParentTotalAcrossLanes(t *testing.T) {
	cache := newAggregateConcurrencyCache()
	svc := NewConcurrencyService(cache)
	proxyID1, proxyID2 := int64(881), int64(882)
	base := Account{
		ID:          88,
		Concurrency: 20,
		ProxyLanes: []AccountProxyLane{
			{ID: 8801, AccountID: 88, ProxyID: &proxyID1, Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 10},
			{ID: 8802, AccountID: 88, ProxyID: &proxyID2, Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 10},
		},
	}
	// Pin the preferred lane on each request-local copy. Once lane 8801 is
	// full, the runtime is allowed to spill to 8802, but every request still
	// consumes one parent account slot.
	preferred := base.ProxyLanes[0]
	leases := make([]func(), 0, 20)
	for i := 0; i < 20; i++ {
		account := base
		account.ProxyLanes = append([]AccountProxyLane(nil), base.ProxyLanes...)
		account.SelectedProxyLane = &preferred
		result, err := acquireAccountProxyLaneSlot(context.Background(), svc, &account, "aggregate-runtime", 20)
		require.NoError(t, err)
		require.Truef(t, result.Acquired, "runtime acquisition %d", i+1)
		leases = append(leases, result.ReleaseFunc)
	}
	require.Equal(t, 20, cache.accountCount(88))
	require.Equal(t, 10, cache.laneCount(8801))
	require.Equal(t, 10, cache.laneCount(8802))

	account := base
	account.ProxyLanes = append([]AccountProxyLane(nil), base.ProxyLanes...)
	account.SelectedProxyLane = &preferred
	result, err := acquireAccountProxyLaneSlot(context.Background(), svc, &account, "aggregate-runtime", 20)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Equal(t, 20, cache.accountCount(88), "the 21st request must be rejected by the parent total")

	for _, release := range leases {
		release()
	}
	require.Equal(t, 0, cache.accountCount(88))
	require.Equal(t, 0, cache.laneCount(8801))
	require.Equal(t, 0, cache.laneCount(8802))
}
