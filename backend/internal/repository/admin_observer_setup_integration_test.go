//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type observerSetupFixture struct {
	ctx         context.Context
	client      *dbent.Client
	users       *userRepository
	groups      *groupRepository
	user        *service.User
	exclusiveID int64
	publicID    int64
}

func newObserverSetupFixture(t *testing.T) *observerSetupFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := testEntClient(t)
	name := fmt.Sprintf("observer-setup-%d", time.Now().UnixNano())
	private, err := client.Group.Create().SetName(name + "-private").SetPlatform(service.PlatformOpenAI).SetIsExclusive(true).Save(ctx)
	require.NoError(t, err)
	public, err := client.Group.Create().SetName(name + "-public").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	users := newUserRepositoryWithSQL(client, integrationDB)
	u := &service.User{Email: name + "@example.test", Username: name, PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive, Concurrency: 5, Balance: 12.5, AllowedGroups: []int64{private.ID, public.ID}}
	require.NoError(t, users.Create(ctx, u))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM redeem_codes WHERE used_by=$1", u.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id=$1", u.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE name LIKE $1", name+"%")
	})
	return &observerSetupFixture{ctx: ctx, client: client, users: users, groups: newGroupRepositoryWithSQL(client, integrationDB), user: u, exclusiveID: private.ID, publicID: public.ID}
}

func (f *observerSetupFixture) admin(codes service.RedeemCodeRepository, invalidator service.APIKeyAuthCacheInvalidator) service.AdminService {
	if codes == nil {
		codes = NewRedeemCodeRepository(f.client)
	}
	return service.NewAdminService(nil, f.users, f.groups, nil, nil, nil, codes, nil, nil, nil, nil, nil, invalidator, f.client, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func (f *observerSetupFixture) reload(t *testing.T) *service.User {
	t.Helper()
	u, err := f.users.GetByID(f.ctx, f.user.ID)
	require.NoError(t, err)
	return u
}

func allObserverSetupOptions() *service.ObserverSetupOptions {
	return &service.ObserverSetupOptions{CreateDedicatedGroup: true, RevokePublicGroups: true, GrantResources: true}
}

type observerSetupInvalidator struct {
	onInvalidate func(context.Context, int64)
}

func (i *observerSetupInvalidator) InvalidateAuthCacheByUserID(ctx context.Context, id int64) {
	i.onInvalidate(ctx, id)
}
func (*observerSetupInvalidator) InvalidateAuthCacheByGroupID(context.Context, int64) {}
func (*observerSetupInvalidator) InvalidateAuthCacheByKey(context.Context, string)    {}

func TestAdminObserverSetup_AllOptionsAndReplay(t *testing.T) {
	f := newObserverSetupFixture(t)
	invalidations := 0
	invalidator := &observerSetupInvalidator{onInvalidate: func(ctx context.Context, id int64) {
		invalidations++
		require.Nil(t, dbent.TxFromContext(ctx), "cache invalidation must happen after commit")
		u, err := f.users.GetByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, service.RoleObserver, u.Role)
		require.True(t, u.RestrictPublicGroups)
	}}
	svc := f.admin(nil, invalidator)
	managed := []int64{f.publicID}
	newName := f.user.Username + "-renamed"
	input := &service.UpdateUserInput{Email: f.user.Email, Role: service.RoleObserver, Username: &newName, ObserverGroupIDs: &managed, ObserverSetup: allObserverSetupOptions(), ActorAdminID: 123}
	updated, err := svc.UpdateUser(f.ctx, f.user.ID, input)
	require.NoError(t, err)
	stored := f.reload(t)
	require.Equal(t, 100011.5, stored.Balance)
	require.Equal(t, 1005, stored.Concurrency)
	require.Equal(t, stored.Balance, updated.Balance)
	group, err := f.client.Group.Query().Where(dbgroup.NameEQ(newName)).Only(f.ctx)
	require.NoError(t, err)
	require.Equal(t, service.PlatformOpenAI, group.Platform)
	require.True(t, group.IsExclusive)
	require.Equal(t, 1.0, group.RateMultiplier)
	require.ElementsMatch(t, []int64{f.publicID, group.ID}, stored.ObserverGroupIDs)
	require.ElementsMatch(t, []int64{f.exclusiveID, group.ID}, stored.AllowedGroups)
	require.False(t, stored.CanBindGroup(f.publicID, false))
	require.False(t, stored.CanBindGroup(999999, false), "future public groups must also be denied")
	require.True(t, stored.CanBindGroup(group.ID, true))
	require.True(t, stored.CanBindGroup(f.exclusiveID, true))
	require.Equal(t, 1, invalidations)
	records, err := f.client.RedeemCode.Query().Where(redeemcode.UsedByEQ(f.user.ID)).All(f.ctx)
	require.NoError(t, err)
	require.Len(t, records, 2)
	values := map[string]float64{}
	for _, r := range records {
		values[r.Type] = r.Value
		require.NotNil(t, r.Notes)
		require.Contains(t, *r.Notes, "actor_admin_id=123")
	}
	require.Equal(t, map[string]float64{service.AdjustmentTypeAdminBalance: 99999, service.AdjustmentTypeAdminConcurrency: 1000}, values)
	_, err = svc.UpdateUser(f.ctx, f.user.ID, input)
	require.Equal(t, "OBSERVER_SETUP_ALREADY_APPLIED", infraerrors.Reason(err))
	require.Equal(t, 100011.5, f.reload(t).Balance)
	require.Equal(t, 1, invalidations)
}

func TestAdminObserverSetup_IndependentOptions(t *testing.T) {
	cases := []struct {
		name    string
		options *service.ObserverSetupOptions
	}{
		{"none", nil},
		{"unchecked", &service.ObserverSetupOptions{}},
		{"group only", &service.ObserverSetupOptions{CreateDedicatedGroup: true}},
		{"public restriction only", &service.ObserverSetupOptions{RevokePublicGroups: true}},
		{"resources only", &service.ObserverSetupOptions{GrantResources: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newObserverSetupFixture(t)
			_, err := f.admin(nil, nil).UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleObserver, ObserverSetup: tc.options})
			require.NoError(t, err)
			u := f.reload(t)
			require.Equal(t, service.RoleObserver, u.Role)
			create, restrict, grant := false, false, false
			if tc.options != nil {
				create, restrict, grant = tc.options.CreateDedicatedGroup, tc.options.RevokePublicGroups, tc.options.GrantResources
			}
			require.Equal(t, restrict, u.RestrictPublicGroups)
			require.Equal(t, !restrict, u.CanBindGroup(f.publicID, false))
			require.Contains(t, u.AllowedGroups, f.exclusiveID)
			if grant {
				require.Equal(t, 100011.5, u.Balance)
				require.Equal(t, 1005, u.Concurrency)
			} else {
				require.Equal(t, 12.5, u.Balance)
				require.Equal(t, 5, u.Concurrency)
			}
			count, err := f.client.Group.Query().Where(dbgroup.NameEQ(f.user.Username)).Count(f.ctx)
			require.NoError(t, err)
			if create {
				require.Equal(t, 1, count)
				require.Len(t, u.ObserverGroupIDs, 1)
			} else {
				require.Zero(t, count)
				require.Empty(t, u.ObserverGroupIDs)
			}
		})
	}
}

func TestAdminObserverSetup_ExistingGroupIsNotReused(t *testing.T) {
	f := newObserverSetupFixture(t)
	existing, err := f.client.Group.Create().SetName(f.user.Username).SetPlatform(service.PlatformOpenAI).Save(f.ctx)
	require.NoError(t, err)
	_, err = f.admin(nil, nil).UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleObserver, ObserverSetup: allObserverSetupOptions()})
	require.ErrorIs(t, err, service.ErrGroupExists)
	u := f.reload(t)
	require.Equal(t, service.RoleUser, u.Role)
	require.Equal(t, 12.5, u.Balance)
	require.Equal(t, 5, u.Concurrency)
	require.False(t, u.RestrictPublicGroups)
	require.ElementsMatch(t, f.user.AllowedGroups, u.AllowedGroups)
	require.NotContains(t, u.AllowedGroups, existing.ID)
	require.Empty(t, u.ObserverGroupIDs)
}

type failingObserverRedeemRepository struct{ service.RedeemCodeRepository }

func (r *failingObserverRedeemRepository) Create(ctx context.Context, code *service.RedeemCode) error {
	if code.Type == service.AdjustmentTypeAdminConcurrency {
		return errors.New("injected adjustment failure")
	}
	return r.RedeemCodeRepository.Create(ctx, code)
}

func TestAdminObserverSetup_RollsBackGroupUserBalanceAndAudit(t *testing.T) {
	f := newObserverSetupFixture(t)
	invalidations := 0
	codes := &failingObserverRedeemRepository{RedeemCodeRepository: NewRedeemCodeRepository(f.client)}
	invalidator := &observerSetupInvalidator{onInvalidate: func(context.Context, int64) { invalidations++ }}
	_, err := f.admin(codes, invalidator).UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Email: f.user.Username + "-updated@example.test", Role: service.RoleObserver, ObserverSetup: allObserverSetupOptions()})
	require.ErrorContains(t, err, "injected adjustment failure")
	u := f.reload(t)
	require.Equal(t, f.user.Email, u.Email)
	require.Equal(t, service.RoleUser, u.Role)
	require.Equal(t, 12.5, u.Balance)
	require.Equal(t, 5, u.Concurrency)
	require.False(t, u.RestrictPublicGroups)
	require.ElementsMatch(t, f.user.AllowedGroups, u.AllowedGroups)
	require.Empty(t, u.ObserverGroupIDs)
	count, err := f.client.Group.Query().Where(dbgroup.NameEQ(f.user.Username)).Count(f.ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = f.client.RedeemCode.Query().Where(redeemcode.UsedByEQ(f.user.ID)).Count(f.ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Zero(t, invalidations)
}

func TestAdminObserverSetup_ConcurrentPromotionGrantsOnce(t *testing.T) {
	f := newObserverSetupFixture(t)
	svc := f.admin(nil, nil)
	start := make(chan struct{})
	results := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleObserver, ObserverSetup: allObserverSetupOptions()})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Equal(t, "OBSERVER_SETUP_ALREADY_APPLIED", infraerrors.Reason(err), "unexpected error: %v", err)
		}
	}
	require.Equal(t, 1, successes)
	u := f.reload(t)
	require.Equal(t, 100011.5, u.Balance)
	require.Equal(t, 1005, u.Concurrency)
	count, err := f.client.Group.Query().Where(dbgroup.NameEQ(f.user.Username)).Count(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = f.client.RedeemCode.Query().Where(redeemcode.UsedByEQ(f.user.ID)).Count(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestAdminObserverSetup_PreservesConcurrentBalanceDeduction(t *testing.T) {
	f := newObserverSetupFixture(t)
	svc := f.admin(nil, nil)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := svc.UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleObserver, ObserverSetup: &service.ObserverSetupOptions{GrantResources: true}})
		results <- err
	}()
	go func() {
		<-start
		_, err := f.users.AdjustBalance(f.ctx, f.user.ID, -5)
		results <- err
	}()
	close(start)
	first, second := <-results, <-results
	require.NoError(t, first)
	require.NoError(t, second)
	u := f.reload(t)
	require.Equal(t, 100006.5, u.Balance)
	require.Equal(t, 1005, u.Concurrency)
}

func TestAdminObserverSetup_ValidationAndUnlimitedConcurrency(t *testing.T) {
	t.Run("requires observer role", func(t *testing.T) {
		f := newObserverSetupFixture(t)
		_, err := f.admin(nil, nil).UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleUser, ObserverSetup: allObserverSetupOptions()})
		require.Equal(t, "OBSERVER_SETUP_REQUIRES_PROMOTION", infraerrors.Reason(err))
		require.Equal(t, service.RoleUser, f.reload(t).Role)
	})
	t.Run("requires username", func(t *testing.T) {
		f := newObserverSetupFixture(t)
		blank := "  "
		_, err := f.admin(nil, nil).UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleObserver, Username: &blank, ObserverSetup: allObserverSetupOptions()})
		require.Equal(t, "OBSERVER_GROUP_USERNAME_REQUIRED", infraerrors.Reason(err))
		u := f.reload(t)
		require.Equal(t, f.user.Username, u.Username)
		require.Equal(t, service.RoleUser, u.Role)
	})
	t.Run("preserves unlimited concurrency", func(t *testing.T) {
		f := newObserverSetupFixture(t)
		unlimited := 0
		u, err := f.admin(nil, nil).UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleObserver, Concurrency: &unlimited, ObserverSetup: &service.ObserverSetupOptions{GrantResources: true}})
		require.NoError(t, err)
		require.Zero(t, u.Concurrency)
		require.Equal(t, 100011.5, u.Balance)
	})
	t.Run("rejects concurrency overflow", func(t *testing.T) {
		f := newObserverSetupFixture(t)
		overflow := 1<<31 - 1
		_, err := f.admin(nil, nil).UpdateUser(f.ctx, f.user.ID, &service.UpdateUserInput{Role: service.RoleObserver, Concurrency: &overflow, ObserverSetup: allObserverSetupOptions()})
		require.Equal(t, "INVALID_OBSERVER_CONCURRENCY", infraerrors.Reason(err))
		u := f.reload(t)
		require.Equal(t, service.RoleUser, u.Role)
		require.Equal(t, 5, u.Concurrency)
	})
}
