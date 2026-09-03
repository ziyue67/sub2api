package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A lane binding must never join a row solely by lane ID.  Although IDs are
// currently generated globally, the account relation is part of the trust
// boundary and protects against stale/corrupt scheduler snapshots.
func TestApplyStickyLaneBindingRequiresLaneAccountOwnership(t *testing.T) {
	account := &Account{
		ID: 10,
		ProxyLanes: []AccountProxyLane{{
			ID:          101,
			AccountID:   11, // mismatched parent must be rejected
			Name:        "foreign",
			Transport:   AccountProxyLaneTransportDirect,
			Status:      AccountProxyLaneStatusActive,
			Schedulable: true,
			Weight:      1,
		}},
	}

	require.False(t, applyStickyLaneBinding(account, LaneStickyBinding{
		AccountID: 10,
		LaneID:    101,
	}, time.Now()))
	require.Nil(t, account.SelectedProxyLane)
}
