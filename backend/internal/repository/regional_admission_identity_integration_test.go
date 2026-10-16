//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/regional/slots"
	"github.com/stretchr/testify/require"
)

func TestRegionalAdmissionCancellationFencesDelayedAcquire(t *testing.T) {
	ctx := context.Background()
	p, l, g := slotLeaseFixture(t)
	require.NoError(t, p.Issue(ctx, g))
	require.NoError(t, l.Install(ctx, g))
	t.Cleanup(func() {
		_, err := integrationDB.Exec("DELETE FROM regional_slot_cancellations WHERE kind=$1 AND resource_id=$2", g.Resource.Kind, g.Resource.ID)
		require.NoError(t, err)
	})
	// Cancellation may precede a delayed acquire and must survive a restart.
	require.NoError(t, p.Release(ctx, g.Resource, "cancel-before-acquire"))
	restartedPrimary := NewRegionalSlotAuthority(integrationDB)
	ok, err := restartedPrimary.Acquire(ctx, g.Resource, "cancel-before-acquire")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, l.ReleaseResource(ctx, g.Resource, "cancel-before-acquire"))
	restartedLocal := &slots.Local{DB: integrationDB, Region: l.Region}
	ok, err = restartedLocal.AcquireResource(ctx, g.Resource, "cancel-before-acquire")
	require.NoError(t, err)
	require.False(t, ok)
	var primary, local int
	require.NoError(t, integrationDB.QueryRow("SELECT primary_used FROM regional_slot_budgets WHERE kind=$1 AND resource_id=$2", g.Resource.Kind, g.Resource.ID).Scan(&primary))
	require.NoError(t, integrationDB.QueryRow("SELECT used FROM regional_local_slot_grants WHERE lease_id=$1", g.LeaseID).Scan(&local))
	require.Zero(t, primary)
	require.Zero(t, local)
	ok, err = restartedPrimary.Acquire(ctx, g.Resource, "fresh-primary")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = restartedLocal.AcquireResource(ctx, g.Resource, "fresh-local")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestRegionalAdmissionConcurrentIdentityAcrossGrants(t *testing.T) {
	ctx := context.Background()
	p, local, first := slotLeaseFixture(t)
	first.Slots = 1
	second := first
	second.LeaseID += "-second"
	require.NoError(t, p.Issue(ctx, first))
	require.NoError(t, p.Issue(ctx, second))
	require.NoError(t, local.Install(ctx, first))
	require.NoError(t, local.Install(ctx, second))
	t.Cleanup(func() {
		_, err := integrationDB.Exec("DELETE FROM regional_slot_cancellations WHERE kind=$1 AND resource_id=$2", first.Resource.Kind, first.Resource.ID)
		require.NoError(t, err)
	})
	type result struct {
		acquired bool
		err      error
	}
	results := make(chan result, 16)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pod := &slots.Local{DB: integrationDB, Region: local.Region}
			ok, err := pod.AcquireResource(ctx, first.Resource, "same-request")
			results <- result{ok, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for r := range results {
		require.NoError(t, r.err)
		require.True(t, r.acquired)
	}
	var held, records int
	require.NoError(t, integrationDB.QueryRow("SELECT sum(used) FROM regional_local_slot_grants WHERE kind=$1 AND resource_id=$2", first.Resource.Kind, first.Resource.ID).Scan(&held))
	require.NoError(t, integrationDB.QueryRow("SELECT count(*) FROM regional_local_slots WHERE kind=$1 AND resource_id=$2 AND request_id='same-request'", first.Resource.Kind, first.Resource.ID).Scan(&records))
	require.Equal(t, 1, held)
	require.Equal(t, 1, records)
	require.NoError(t, local.ReleaseResource(ctx, first.Resource, "same-request"))
	ok, err := local.AcquireResource(ctx, first.Resource, "same-request")
	require.NoError(t, err)
	require.False(t, ok, "released request must not move to another grant")
	for _, id := range []string{"fresh-one", "fresh-two"} {
		ok, err = local.AcquireResource(ctx, first.Resource, id)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err = local.AcquireResource(ctx, first.Resource, "over-capacity")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRegionalAdmissionCompositeTransactionRollback(t *testing.T) {
	ctx := context.Background()
	p, l, g := slotLeaseFixture(t)
	require.NoError(t, p.Issue(ctx, g))
	require.NoError(t, l.Install(ctx, g))
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	ok, err := p.AcquireTx(ctx, tx, g.Resource, "composite-primary")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = l.AcquireResourceTx(ctx, tx, g.Resource, "composite-local")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, tx.Rollback())
	// A failed composite admission must not leave either half allocated.
	var primary, local int
	require.NoError(t, integrationDB.QueryRow("SELECT primary_used FROM regional_slot_budgets WHERE kind=$1 AND resource_id=$2", g.Resource.Kind, g.Resource.ID).Scan(&primary))
	require.NoError(t, integrationDB.QueryRow("SELECT used FROM regional_local_slot_grants WHERE lease_id=$1", g.LeaseID).Scan(&local))
	require.Zero(t, primary)
	require.Zero(t, local)
	ok, err = p.Acquire(ctx, g.Resource, "composite-primary")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = l.AcquireResource(ctx, g.Resource, "composite-local")
	require.NoError(t, err)
	require.True(t, ok)
}
