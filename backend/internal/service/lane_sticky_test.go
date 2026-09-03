package service

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplyStickyLaneBindingRequiresOwnedSchedulableLane(t *testing.T) {
	account := &Account{
		ID: 10,
		ProxyLanes: []AccountProxyLane{
			{ID: 101, AccountID: 10, Name: "a", Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1},
			{ID: 102, AccountID: 10, Name: "b", Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusPaused, Schedulable: true, Weight: 1},
		},
	}
	require.True(t, applyStickyLaneBinding(account, LaneStickyBinding{AccountID: 10, LaneID: 101}, time.Now()))
	require.NotNil(t, account.SelectedProxyLane)
	require.Equal(t, int64(101), account.SelectedProxyLane.ID)
	require.False(t, applyStickyLaneBinding(account, LaneStickyBinding{AccountID: 10, LaneID: 102}, time.Now()))
	require.False(t, applyStickyLaneBinding(account, LaneStickyBinding{AccountID: 11, LaneID: 101}, time.Now()))
}

func TestApplyStickyLaneBindingPreservesAggregateConcurrency(t *testing.T) {
	account := &Account{
		ID:          10,
		Concurrency: 20,
		ProxyLanes: []AccountProxyLane{
			{ID: 101, AccountID: 10, Transport: AccountProxyLaneTransportDirect, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 10},
		},
	}
	require.True(t, applyStickyLaneBinding(account, LaneStickyBinding{AccountID: 10, LaneID: 101}, time.Now()))
	require.NotNil(t, account.SelectedProxyLane)
	require.Equal(t, 10, account.SelectedProxyLane.Concurrency)
	// Sticky selection is an affinity hint before admission; account.Concurrency
	// must remain the parent ceiling so the composite acquire does not silently
	// downgrade total=20 to the selected lane's cap=10.
	require.Equal(t, 20, account.Concurrency)
}

func TestIsLaneStickyTransportFailureNarrowTaxonomy(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		want   bool
	}{
		{name: "connection refused", err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), want: true},
		{name: "network timeout", err: &net.DNSError{Err: "timeout", IsTimeout: true}, want: true},
		{name: "client cancellation", err: context.Canceled, want: false},
		{name: "local request deadline", err: context.DeadlineExceeded, want: false},
		{name: "business bad request", status: 400, want: false},
		{name: "auth forbidden", status: 403, want: false},
		{name: "bare gateway status", status: 502, want: false},
		{name: "bare unavailable status", status: 503, want: false},
		{name: "bare timeout status", status: 504, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsLaneStickyTransportFailure(tt.err, tt.status))
		})
	}
}

func TestLaneStickyCacheOptionalAdapterDoesNotRequireGatewayCacheMethods(t *testing.T) {
	// Existing adapters implement GatewayCache only.  The optional type
	// assertion must return nil rather than forcing every adapter to grow four
	// new methods.
	var cache GatewayCache = &laneStickyGatewayCacheStub{}
	require.Nil(t, laneStickyCache(cache))
}

type laneStickyGatewayCacheStub struct{}

func (*laneStickyGatewayCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, ErrStickySessionNotFound
}
func (*laneStickyGatewayCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (*laneStickyGatewayCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (*laneStickyGatewayCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}
func (*laneStickyGatewayCacheStub) SetGrokVideoPendingBilling(context.Context, string, []byte, time.Duration) error {
	return nil
}
func (*laneStickyGatewayCacheStub) GetGrokVideoPendingBilling(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (*laneStickyGatewayCacheStub) ClaimGrokVideoBilled(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}
func (*laneStickyGatewayCacheStub) ReleaseGrokVideoBilled(context.Context, string) error { return nil }
func (*laneStickyGatewayCacheStub) SetReasoningContent(context.Context, string, string, time.Duration) error {
	return nil
}
func (*laneStickyGatewayCacheStub) GetReasoningContent(context.Context, string) (string, error) {
	return "", ErrReasoningContentNotFound
}
