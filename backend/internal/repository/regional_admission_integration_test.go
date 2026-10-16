//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"github.com/Wei-Shaw/sub2api/internal/regional/slots"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRegionalAdmissionCancellationFencesLateAcquire(t *testing.T) {
	ctx := context.Background()
	p, l, g := slotLeaseFixture(t)
	require.NoError(t, p.Issue(ctx, g))
	require.NoError(t, l.Install(ctx, g))
	require.NoError(t, p.Release(ctx, g.Resource, "late"))
	ok, err := p.Acquire(ctx, g.Resource, "late")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, l.ReleaseResource(ctx, g.Resource, "late"))
	ok, err = l.AcquireResource(ctx, g.Resource, "late")
	require.NoError(t, err)
	require.False(t, ok)
}
func regionalAdmissionCache(t *testing.T) *regionalConcurrencyCache {
	t.Helper()
	p := &slots.Primary{DB: integrationDB, Limit: func(context.Context, *sql.Tx, slots.Resource) (int, error) { return 1, nil }}
	cache, err := NewRegionalConcurrencyCache(NewConcurrencyCache(testRedis(t), 15, 30), p, nil)
	require.NoError(t, err)
	return cache.(*regionalConcurrencyCache)
}
func TestRegionalAdmissionFIFOAtomicCancelAndReplay(t *testing.T) {
	ctx := context.Background()
	c := regionalAdmissionCache(t)
	id := time.Now().UnixNano()
	ok, now, err := c.AcquireAPIKeySlotWithTime(ctx, id, 1, "active")
	require.NoError(t, err)
	require.True(t, ok)
	deadline := now + 30000
	for _, r := range []string{"first", "second"} {
		out, err := c.AdvanceAPIKeyQueue(ctx, id, r, service.APIKeyQueueModeEnter, deadline, 1, 5)
		require.NoError(t, err)
		require.Equal(t, service.APIKeyQueueOutcomeWaiting, out)
	}
	require.NoError(t, c.ReleaseAPIKeySlot(ctx, id, "active"))
	out, err := c.AdvanceAPIKeyQueue(ctx, id, "second", service.APIKeyQueueModePoll, deadline, 1, 5)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyQueueOutcomeWaiting, out)
	out, err = c.AdvanceAPIKeyQueue(ctx, id, "first", service.APIKeyQueueModePoll, deadline, 1, 5)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyQueueOutcomeAcquired, out)
	require.NoError(t, c.AbortAPIKeyQueueAttempt(ctx, id, "first", deadline, true))
	out, err = c.AdvanceAPIKeyQueue(ctx, id, "first", service.APIKeyQueueModePoll, deadline, 1, 5)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyQueueOutcomeAborted, out)
	out, err = c.AdvanceAPIKeyQueue(ctx, id, "second", service.APIKeyQueueModePoll, deadline, 1, 5)
	require.NoError(t, err)
	require.Equal(t, service.APIKeyQueueOutcomeAcquired, out)
	require.NoError(t, c.AbortAPIKeyQueueAttempt(ctx, id, "late", deadline, true))
	ok, _, err = c.AcquireAPIKeySlotWithTime(ctx, id, 1, "late")
	require.NoError(t, err)
	require.False(t, ok)
}
func TestRegionalAdmissionLiveExactTransferMigrationAndAbort(t *testing.T) {
	ctx := context.Background()
	c := regionalAdmissionCache(t)
	id := time.Now().UnixNano()
	r := service.LiveLeaseTransferRequest{AccountID: id, AccountMax: 1, AccountRequestID: "a", UserID: id, UserMax: 1, UserRequestID: "u", APIKeyID: id, APIKeyMax: 1, KeyRequestID: "k", LeaseID: fmt.Sprint(id)}
	for _, m := range liveMembers(r) {
		ok, err := c.acquire(ctx, m.Resource.Kind, m.Resource.ID, m.Source)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := c.AcquireLiveLeaseTransferring(ctx, r)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireLiveLeaseTransferring(ctx, r)
	require.NoError(t, err)
	require.True(t, ok)
	for _, m := range liveMembers(r) {
		require.NoError(t, c.release(ctx, m.Resource.Kind, m.Resource.ID, m.Source))
		n, err := c.count(ctx, m.Resource.Kind, m.Resource.ID)
		require.NoError(t, err)
		require.Equal(t, 1, n)
	}
	next := r
	next.ReplacedAccountID = id
	next.AccountID++
	next.AccountRequestID = "new-account"
	next.UserMax = 0
	next.APIKeyMax = 0
	next.UserRequestID = ""
	next.KeyRequestID = ""
	ok, err = c.AcquireAccountSlot(ctx, next.AccountID, 1, next.AccountRequestID)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.MigrateLiveLeaseAccount(ctx, next)
	require.NoError(t, err)
	require.True(t, ok)
	n, err := c.GetAccountConcurrency(ctx, id)
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, c.AbortLiveLeaseTransfer(ctx, next))
	ok, err = c.MigrateLiveLeaseAccount(ctx, next)
	require.ErrorIs(t, err, service.ErrLiveLeaseTransferFenced)
	require.False(t, ok)
	n, err = c.GetUserConcurrency(ctx, id)
	require.NoError(t, err)
	require.Zero(t, n)
}

type lostRegionalBrokerReply struct {
	p    *slots.Primary
	lost bool
}

func (r *lostRegionalBrokerReply) IssueSlots(ctx context.Context, g slots.Grant) error {
	if err := r.p.Issue(ctx, g); err != nil {
		return err
	}
	if !r.lost {
		r.lost = true
		return errors.New("simulated lost acknowledgement")
	}
	return nil
}
func (r *lostRegionalBrokerReply) RenewSlots(ctx context.Context, g slots.Grant) error {
	return r.p.Renew(ctx, g)
}
func (r *lostRegionalBrokerReply) IssueQuota(context.Context, regional.Lease) error {
	return errors.New("not used")
}
func TestRegionalAdmissionBrokerLostReplyReplaysOriginalIdentity(t *testing.T) {
	ctx := context.Background()
	p, l, g := slotLeaseFixture(t)
	remote := &lostRegionalBrokerReply{p: p}
	b := &control.Broker{DB: integrationDB, Remote: remote, Slots: l}
	require.NoError(t, b.Queue(ctx, g.LeaseID, "slots/issue", g))
	require.Error(t, b.Replay(ctx, g.LeaseID))
	restarted := &control.Broker{DB: integrationDB, Remote: remote, Slots: l}
	require.NoError(t, restarted.RunOnce(ctx))
	var granted int
	require.NoError(t, integrationDB.QueryRow("SELECT granted FROM regional_slot_budgets WHERE kind=$1 AND resource_id=$2", g.Resource.Kind, g.Resource.ID).Scan(&granted))
	require.Equal(t, g.Slots, granted)
	changed := g
	changed.Slots++
	require.ErrorIs(t, b.Queue(ctx, g.LeaseID, "slots/issue", changed), slots.ErrConflict)
	ok, err := l.AcquireResource(ctx, g.Resource, "after-restart")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestRegionalAdmissionMultipleGrantsOneRequest(t *testing.T) {
	ctx := context.Background()
	p, l, g := slotLeaseFixture(t)
	require.NoError(t, p.Issue(ctx, g))
	require.NoError(t, l.Install(ctx, g))
	other := g
	other.LeaseID += "-second"
	require.NoError(t, p.Issue(ctx, other))
	require.NoError(t, l.Install(ctx, other))
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := l.AcquireResource(ctx, g.Resource, "same-request")
			if err == nil && !ok {
				err = errors.New("same request was unexpectedly rejected")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	n, err := l.Count(ctx, g.Resource)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, l.Release(ctx, g.LeaseID, "cancel-direct"))
	ok, err := l.Acquire(ctx, g.LeaseID, "cancel-direct")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRegionalAdmissionGrantGuardRequiresCompleteFleetAndRevocation(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	epoch := fmt.Sprint(time.Now().UnixNano())
	legacy := "legacy-" + epoch
	require.NoError(t, rdb.Do(ctx, "ACL", "SETUSER", legacy, "on", "nopass", "~*", "+@all").Err())
	t.Cleanup(func() { _ = rdb.Do(ctx, "ACL", "DELUSER", legacy).Err() })
	cfg := RegionalConcurrencyRuntimeConfig{Mode: "primary", Epoch: epoch, Instance: "pod-a", PrimaryInstances: []string{"pod-a", "pod-b"}, LegacyRedisUser: legacy}
	guard := regionalLegacyGuard(rdb, cfg)
	check := func() error {
		tx, err := integrationDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		return guard(ctx, tx)
	}
	require.Error(t, check())
	require.NoError(t, rdb.Do(ctx, "ACL", "SETUSER", legacy, "off").Err())
	require.Error(t, check())
	_, err := integrationDB.Exec("INSERT INTO regional_runtime_members(epoch,instance_id,role) VALUES($1,'pod-a','primary'),($1,'pod-b','primary')", epoch)
	require.NoError(t, err)
	require.NoError(t, check())
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(123), redis.Z{Score: 1, Member: "undrained"}).Err())
	require.Error(t, check())
	require.NoError(t, rdb.Del(ctx, accountSlotKey(123)).Err())
	_, err = integrationDB.Exec("UPDATE regional_runtime_members SET heartbeat_at=clock_timestamp()-interval '1 minute' WHERE epoch=$1 AND instance_id='pod-b'", epoch)
	require.NoError(t, err)
	require.Error(t, check())
}

func TestRegionalAdmissionBrokerExpiredUnissuedHasDurableRejection(t *testing.T) {
	ctx := context.Background()
	p, l, g := slotLeaseFixture(t)
	g.IssuedAt = g.IssuedAt.Add(-time.Minute)
	b := &control.Broker{DB: integrationDB, Remote: &lostRegionalBrokerReply{p: p, lost: true}, Slots: l}
	require.NoError(t, b.Queue(ctx, g.LeaseID, "slots/issue", g))
	require.NoError(t, b.Replay(ctx, g.LeaseID))
	var state string
	require.NoError(t, integrationDB.QueryRow("SELECT state FROM regional_broker_operations WHERE operation_id=$1", g.LeaseID).Scan(&state))
	require.Equal(t, "rejected", state)
	require.ErrorIs(t, p.Issue(ctx, g), slots.ErrExpiredUnissued)
	altered := g
	altered.IssuedAt = altered.IssuedAt.Add(time.Second)
	require.ErrorIs(t, p.Issue(ctx, altered), slots.ErrConflict)
	n, err := p.Count(ctx, g.Resource)
	require.NoError(t, err)
	require.Zero(t, n)
}
