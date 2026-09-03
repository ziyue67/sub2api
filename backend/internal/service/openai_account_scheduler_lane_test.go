package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// schedulerLaneCapabilityCache deliberately embeds the existing scheduler
// test double so it keeps the legacy ConcurrencyCache contract while exposing
// the optional lane namespace.  The scheduler must not use the account-level
// load snapshot as a full gate when this capability is present.
type schedulerLaneCapabilityCache struct {
	schedulerTestConcurrencyCache
}

func (schedulerLaneCapabilityCache) AcquireLaneSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}

func (schedulerLaneCapabilityCache) ReleaseLaneSlot(context.Context, int64, string) error {
	return nil
}

func (schedulerLaneCapabilityCache) GetLaneConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}

func (schedulerLaneCapabilityCache) IncrementLaneWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}

func (schedulerLaneCapabilityCache) DecrementLaneWaitCount(context.Context, int64) error {
	return nil
}

func (schedulerLaneCapabilityCache) GetLaneWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}

func TestOpenAIAccountLoadKnownFullLaneAccountUsesLaneAdmission(t *testing.T) {
	laneProxyID := int64(701)
	account := &Account{
		ID:          700,
		Concurrency: 1, // stale legacy bucket appears full below
		ProxyLanes: []AccountProxyLane{{
			ID:          7001,
			AccountID:   700,
			ProxyID:     &laneProxyID,
			Name:        "edge-a",
			Transport:   AccountProxyLaneTransportProxy,
			Status:      AccountProxyLaneStatusActive,
			Schedulable: true,
			Weight:      1,
			Concurrency: 2,
		}},
	}
	candidate := openAIAccountCandidateScore{
		account:   account,
		loadKnown: true,
		loadInfo:  &AccountLoadInfo{AccountID: account.ID, CurrentConcurrency: 1, LoadRate: 100},
	}

	laneScheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{
		concurrencyService: NewConcurrencyService(schedulerLaneCapabilityCache{}),
	}}
	require.True(t, laneConcurrencySupported(laneScheduler.service.concurrencyService))
	require.False(t, laneScheduler.isOpenAIAccountLoadKnownFull(candidate),
		"legacy account load must not discard a lane-enabled account")

	legacyScheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}}
	require.False(t, laneConcurrencySupported(legacyScheduler.service.concurrencyService))
	require.True(t, legacyScheduler.isOpenAIAccountLoadKnownFull(candidate),
		"without lane capability the legacy account full gate remains valid")
}

func TestOpenAIAccountLoadKnownFullUnlimitedAndUnknownAreNotSkipped(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}}
	for _, tc := range []struct {
		name        string
		loadKnown   bool
		concurrency int
	}{
		{name: "unknown snapshot", loadKnown: false, concurrency: 1},
		{name: "unlimited account", loadKnown: true, concurrency: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := openAIAccountCandidateScore{
				account:   &Account{ID: 800, Concurrency: tc.concurrency},
				loadKnown: tc.loadKnown,
				loadInfo:  &AccountLoadInfo{AccountID: 800, CurrentConcurrency: 999},
			}
			require.False(t, scheduler.isOpenAIAccountLoadKnownFull(candidate))
		})
	}
}

func TestOpenAIAccountLoadKnownFullLaneAccountWithoutConcurrencyServiceIsFailOpen(t *testing.T) {
	account := &Account{
		ID:          801,
		Concurrency: 1,
		ProxyLanes: []AccountProxyLane{{
			ID:          8011,
			AccountID:   801,
			Name:        "edge-a",
			Transport:   AccountProxyLaneTransportDirect,
			Status:      AccountProxyLaneStatusActive,
			Schedulable: true,
			Weight:      1,
			Concurrency: 1,
		}},
	}
	candidate := openAIAccountCandidateScore{
		account:   account,
		loadKnown: true,
		loadInfo:  &AccountLoadInfo{AccountID: account.ID, CurrentConcurrency: 1},
	}
	// With no admission service, there is no authoritative Redis bucket to
	// consult.  A stale load snapshot must not make the scheduler reject this
	// lane-enabled candidate.
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{}}
	require.False(t, scheduler.isOpenAIAccountLoadKnownFull(candidate))
}

func TestReconcileOpenAISelectedLaneRejectsOwnershipAndTransportChanges(t *testing.T) {
	proxyID := int64(901)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8901, Status: StatusActive}
	base := AccountProxyLane{
		ID: 9001, AccountID: 90, ProxyID: &proxyID, Proxy: proxy,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive,
		Schedulable: true, Weight: 1, Concurrency: 2,
	}
	cases := []struct {
		name    string
		account *Account
		result  *AcquireResult
	}{
		{
			name:    "cross account result",
			account: &Account{ID: 90, ProxyLanes: []AccountProxyLane{base}},
			result:  &AcquireResult{Acquired: true, Lane: func() *AccountProxyLane { l := base; l.AccountID = 91; return &l }()},
		},
		{
			name: "cross account row",
			account: &Account{ID: 90, ProxyLanes: []AccountProxyLane{func() AccountProxyLane {
				l := base
				l.AccountID = 91
				return l
			}()}},
			result: &AcquireResult{Acquired: true, Lane: &base},
		},
		{
			name: "transport changed",
			account: &Account{ID: 90, ProxyLanes: []AccountProxyLane{func() AccountProxyLane {
				l := base
				l.Transport = AccountProxyLaneTransportDirect
				l.ProxyID = nil
				l.Proxy = nil
				return l
			}()}},
			result: &AcquireResult{Acquired: true, Lane: &base},
		},
		{
			name: "proxy rebound",
			account: &Account{ID: 90, ProxyLanes: []AccountProxyLane{func() AccountProxyLane {
				l := base
				id := int64(902)
				l.ProxyID = &id
				l.Proxy = &Proxy{ID: id, Protocol: "http", Host: "other.example", Port: 8902, Status: StatusActive}
				return l
			}()}},
			result: &AcquireResult{Acquired: true, Lane: &base},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			valid, changed := reconcileOpenAISelectedLane(tc.account, tc.result)
			require.False(t, valid)
			require.False(t, changed)
		})
	}
}

func TestReconcileOpenAISelectedLaneAcceptsMatchingProxyLane(t *testing.T) {
	proxyID := int64(903)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8903, Status: StatusActive}
	lane := &AccountProxyLane{
		ID: 9003, AccountID: 93, ProxyID: &proxyID, Proxy: proxy,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive,
		Schedulable: true, Weight: 1, Concurrency: 3,
	}
	account := &Account{ID: 93, ProxyLanes: []AccountProxyLane{*lane}}
	valid, changed := reconcileOpenAISelectedLane(account, &AcquireResult{Acquired: true, Lane: lane})
	require.True(t, valid)
	require.False(t, changed)
	require.NotNil(t, account.SelectedProxyLane)
	require.Equal(t, lane.ID, account.SelectedProxyLane.ID)
}
