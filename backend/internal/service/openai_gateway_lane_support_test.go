package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// openAILaneLoadCache is deliberately small: it embeds the existing scheduler
// test cache for the legacy ConcurrencyCache contract and supplies only the
// optional lane counters used by refreshOpenAILaneLoadMap.
type openAILaneLoadCache struct {
	schedulerTestConcurrencyCache
	counts   map[int64]int
	waits    map[int64]int
	batchErr error
}

func (c *openAILaneLoadCache) AcquireLaneSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (c *openAILaneLoadCache) ReleaseLaneSlot(context.Context, int64, string) error { return nil }
func (c *openAILaneLoadCache) GetLaneConcurrency(_ context.Context, laneID int64) (int, error) {
	return c.counts[laneID], nil
}
func (c *openAILaneLoadCache) GetLaneConcurrencyBatch(context.Context, []int64) (map[int64]int, error) {
	if c.batchErr != nil {
		return nil, c.batchErr
	}
	result := make(map[int64]int, len(c.counts))
	for laneID, count := range c.counts {
		result[laneID] = count
	}
	return result, nil
}
func (c *openAILaneLoadCache) IncrementLaneWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (c *openAILaneLoadCache) DecrementLaneWaitCount(context.Context, int64) error { return nil }
func (c *openAILaneLoadCache) GetLaneWaitingCount(_ context.Context, laneID int64) (int, error) {
	return c.waits[laneID], nil
}

func TestRefreshOpenAILaneLoadMapUsesAggregateCapacity(t *testing.T) {
	cache := &openAILaneLoadCache{
		counts: map[int64]int{101: 2, 102: 0},
		waits:  map[int64]int{101: 1, 102: 0},
	}
	svc := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache)}
	account := &Account{ID: 1, ProxyLanes: []AccountProxyLane{
		{ID: 101, AccountID: 1, Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 2},
		{ID: 102, AccountID: 1, Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 2},
	}}
	loadMap := map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: 99, WaitingCount: 99, LoadRate: 100}}

	svc.refreshOpenAILaneLoadMap(context.Background(), []*Account{account}, loadMap)
	load := loadMap[1]
	require.NotNil(t, load)
	// Sum of lane counters is 2 and the aggregate waiting count is 1;
	// aggregate capped capacity is 4, so the idle sibling lane keeps this
	// account below the full pre-filter threshold.
	require.Equal(t, 2, load.CurrentConcurrency)
	require.Equal(t, 1, load.WaitingCount)
	require.Equal(t, 75, load.LoadRate)
}

func TestRefreshOpenAILaneLoadMapUnlimitedLaneRemainsAvailable(t *testing.T) {
	cache := &openAILaneLoadCache{counts: map[int64]int{201: 5, 202: 100}}
	svc := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache)}
	account := &Account{ID: 2, ProxyLanes: []AccountProxyLane{
		{ID: 201, AccountID: 2, Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 0},
		{ID: 202, AccountID: 2, Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 1},
	}}
	loadMap := map[int64]*AccountLoadInfo{2: {AccountID: 2, LoadRate: 100}}

	svc.refreshOpenAILaneLoadMap(context.Background(), []*Account{account}, loadMap)
	load := loadMap[2]
	require.NotNil(t, load)
	require.Equal(t, 105, load.CurrentConcurrency)
	require.Zero(t, load.LoadRate, "an unlimited healthy lane keeps admission available")
}

func TestRefreshOpenAILaneLoadMapMarksNoHealthyLaneFull(t *testing.T) {
	cache := &openAILaneLoadCache{}
	svc := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache)}
	account := &Account{ID: 3, ProxyLanes: []AccountProxyLane{
		{ID: 301, AccountID: 3, Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusPaused, Schedulable: true, Weight: 1, Concurrency: 1},
	}}
	loadMap := map[int64]*AccountLoadInfo{3: {AccountID: 3, LoadRate: 0}}

	svc.refreshOpenAILaneLoadMap(context.Background(), []*Account{account}, loadMap)
	require.Equal(t, 100, loadMap[3].LoadRate)
}

func TestRefreshOpenAILaneLoadMapLeavesLegacySnapshotUntouched(t *testing.T) {
	cache := &openAILaneLoadCache{}
	svc := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache)}
	account := &Account{ID: 4, Concurrency: 2}
	loadMap := map[int64]*AccountLoadInfo{4: {AccountID: 4, CurrentConcurrency: 1, WaitingCount: 2, LoadRate: 150}}

	svc.refreshOpenAILaneLoadMap(context.Background(), []*Account{account}, loadMap)
	require.Equal(t, &AccountLoadInfo{AccountID: 4, CurrentConcurrency: 1, WaitingCount: 2, LoadRate: 150}, loadMap[4])
}

func TestRefreshOpenAILaneLoadMapReadErrorAllowsAtomicLaneAdmission(t *testing.T) {
	cache := &openAILaneLoadCache{batchErr: context.DeadlineExceeded}
	svc := &OpenAIGatewayService{concurrencyService: NewConcurrencyService(cache)}
	account := &Account{ID: 5, ProxyLanes: []AccountProxyLane{
		{ID: 501, AccountID: 5, Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 1},
	}}
	loadMap := map[int64]*AccountLoadInfo{5: {AccountID: 5, CurrentConcurrency: 1, LoadRate: 100}}

	// Redis read failure must not preserve the stale account-level full marker;
	// the subsequent atomic AcquireLaneSlot call is the source of truth.
	svc.refreshOpenAILaneLoadMap(context.Background(), []*Account{account}, loadMap)
	require.NotNil(t, loadMap[5])
	require.Zero(t, loadMap[5].LoadRate)
}
