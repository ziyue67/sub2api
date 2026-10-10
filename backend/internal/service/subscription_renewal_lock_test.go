package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

type lockingRenewalRepo struct {
	userSubRepoNoop
	mu        sync.Mutex
	stale     UserSubscription
	current   UserSubscription
	lockReads int
}

func (r *lockingRenewalRepo) ExistsByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	return true, nil
}

func (r *lockingRenewalRepo) GetByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	copy := r.stale
	return &copy, nil
}

func (r *lockingRenewalRepo) GetByID(_ context.Context, _ int64) (*UserSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := r.current
	return &copy, nil
}

func (r *lockingRenewalRepo) GetByIDForUpdate(_ context.Context, _ int64) (*UserSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lockReads++
	copy := r.current
	return &copy, nil
}

func (r *lockingRenewalRepo) ExtendExpiry(_ context.Context, _ int64, expiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current.ExpiresAt = expiresAt
	return nil
}

func (r *lockingRenewalRepo) UpdateStatus(_ context.Context, _ int64, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current.Status = status
	return nil
}

func (r *lockingRenewalRepo) UpdateNotes(_ context.Context, _ int64, notes string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current.Notes = notes
	return nil
}

func (r *lockingRenewalRepo) Update(_ context.Context, sub *UserSubscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = *sub
	return nil
}

func TestAssignOrExtendSubscriptionUsesLockedCurrentRow(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	lockedExpiry := now.AddDate(0, 0, 20)
	windowStart := now.Add(-24 * time.Hour)
	repo := &lockingRenewalRepo{
		stale: UserSubscription{ID: 7, UserID: 11, GroupID: 13, ExpiresAt: now.Add(-time.Hour), Status: SubscriptionStatusExpired, Notes: "stale"},
		current: UserSubscription{
			ID: 7, UserID: 11, GroupID: 13, StartsAt: now.AddDate(0, 0, -10), ExpiresAt: lockedExpiry,
			Status: SubscriptionStatusSuspended, Notes: "current", DailyWindowStart: &windowStart, DailyUsageUSD: 4,
		},
	}
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: &Group{ID: 13, SubscriptionType: SubscriptionTypeSubscription}}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	sub, extended, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 11, GroupID: 13, ValidityDays: 5, Notes: "renewed",
	})

	require.NoError(t, err)
	require.True(t, extended)
	require.Equal(t, 1, repo.lockReads)
	require.Equal(t, lockedExpiry.AddDate(0, 0, 5), sub.ExpiresAt)
	require.Equal(t, SubscriptionStatusActive, sub.Status)
	require.Equal(t, "current\nrenewed", sub.Notes)
	require.Equal(t, windowStart, *sub.DailyWindowStart)
	require.Equal(t, float64(4), sub.DailyUsageUSD)
}

func TestAssignOrExtendSubscriptionSerializedRenewalsAccumulateDays(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	initialExpiry := now.AddDate(0, 0, 10)
	stale := UserSubscription{ID: 17, UserID: 21, GroupID: 23, StartsAt: now, ExpiresAt: initialExpiry, Status: SubscriptionStatusActive}
	repo := &lockingRenewalRepo{stale: stale, current: stale}
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: &Group{ID: 23, SubscriptionType: SubscriptionTypeSubscription}}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }
	input := &AssignSubscriptionInput{UserID: 21, GroupID: 23, ValidityDays: 7}

	_, _, err := svc.AssignOrExtendSubscription(context.Background(), input)
	require.NoError(t, err)
	second, _, err := svc.AssignOrExtendSubscription(context.Background(), input)
	require.NoError(t, err)

	require.Equal(t, 2, repo.lockReads)
	require.Equal(t, initialExpiry.AddDate(0, 0, 14), second.ExpiresAt)
}

func TestPaidSubscriptionRestartReplacesTermAndRefillsQuota(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	oldStart := now.AddDate(0, 0, -10)
	oldExpiry := now.AddDate(0, 0, 20)
	oldDailyWindow := now.AddDate(0, 0, -1)
	oldPeriodicWindow := now.AddDate(0, 0, -7)
	current := UserSubscription{
		ID: 47, UserID: 51, GroupID: 53, StartsAt: oldStart, ExpiresAt: oldExpiry,
		Status: SubscriptionStatusActive, Notes: "old",
		DailyWindowStart: &oldDailyWindow, WeeklyWindowStart: &oldPeriodicWindow, MonthlyWindowStart: &oldPeriodicWindow,
		DailyUsageUSD: 4, WeeklyUsageUSD: 8, MonthlyUsageUSD: 12,
	}
	repo := &lockingRenewalRepo{stale: current, current: current}
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: &Group{ID: 53, SubscriptionType: SubscriptionTypeSubscription}}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	sub, renewed, err := svc.assignOrRenewPaidSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 51, GroupID: 53, ValidityDays: 30, Notes: "payment order 9",
	}, SubscriptionRenewalModeRestart, false)

	require.NoError(t, err)
	require.True(t, renewed)
	require.Equal(t, 1, repo.lockReads)
	require.Equal(t, now, sub.StartsAt)
	require.Equal(t, now.AddDate(0, 0, 30), sub.ExpiresAt)
	require.Equal(t, timezone.StartOfDay(now), *sub.DailyWindowStart)
	require.Equal(t, now, *sub.WeeklyWindowStart)
	require.Equal(t, now, *sub.MonthlyWindowStart)
	require.Zero(t, sub.DailyUsageUSD)
	require.Zero(t, sub.WeeklyUsageUSD)
	require.Zero(t, sub.MonthlyUsageUSD)
	require.Equal(t, "old\npayment order 9", sub.Notes)
}

func TestPaidSubscriptionExtendPreservesCurrentTermAndQuota(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	oldStart := now.AddDate(0, 0, -10)
	oldExpiry := now.AddDate(0, 0, 20)
	windowStart := now.AddDate(0, 0, -1)
	current := UserSubscription{
		ID: 57, UserID: 61, GroupID: 63, StartsAt: oldStart, ExpiresAt: oldExpiry,
		Status: SubscriptionStatusActive, DailyWindowStart: &windowStart, DailyUsageUSD: 4,
	}
	repo := &lockingRenewalRepo{stale: current, current: current}
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: &Group{ID: 63, SubscriptionType: SubscriptionTypeSubscription}}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	sub, renewed, err := svc.assignOrRenewPaidSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 61, GroupID: 63, ValidityDays: 30,
	}, SubscriptionRenewalModeExtend, false)

	require.NoError(t, err)
	require.True(t, renewed)
	require.Equal(t, oldStart, sub.StartsAt)
	require.Equal(t, oldExpiry.AddDate(0, 0, 30), sub.ExpiresAt)
	require.Equal(t, windowStart, *sub.DailyWindowStart)
	require.Equal(t, 4.0, sub.DailyUsageUSD)
}

func TestPaidSubscriptionExtendRestartsExpiredSubscription(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	oldWindow := now.AddDate(0, 0, -20)
	current := UserSubscription{
		ID: 67, UserID: 71, GroupID: 73, StartsAt: now.AddDate(0, 0, -30), ExpiresAt: now.Add(-time.Hour),
		Status: SubscriptionStatusActive, DailyWindowStart: &oldWindow, WeeklyWindowStart: &oldWindow, MonthlyWindowStart: &oldWindow,
		DailyUsageUSD: 4, WeeklyUsageUSD: 8, MonthlyUsageUSD: 12,
	}
	repo := &lockingRenewalRepo{stale: current, current: current}
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: &Group{ID: 73, SubscriptionType: SubscriptionTypeSubscription}}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	sub, renewed, err := svc.assignOrRenewPaidSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 71, GroupID: 73, ValidityDays: 30,
	}, SubscriptionRenewalModeExtend, false)

	require.NoError(t, err)
	require.True(t, renewed)
	require.Equal(t, now, sub.StartsAt)
	require.Equal(t, now.AddDate(0, 0, 30), sub.ExpiresAt)
	require.Equal(t, timezone.StartOfDay(now), *sub.DailyWindowStart)
	require.Equal(t, now, *sub.WeeklyWindowStart)
	require.Equal(t, now, *sub.MonthlyWindowStart)
	require.Zero(t, sub.DailyUsageUSD)
	require.Zero(t, sub.WeeklyUsageUSD)
	require.Zero(t, sub.MonthlyUsageUSD)
}

func TestNormalizeSubscriptionRenewalMode(t *testing.T) {
	mode, err := NormalizeSubscriptionRenewalMode("")
	require.NoError(t, err)
	require.Equal(t, SubscriptionRenewalModeRestart, mode)

	mode, err = NormalizeSubscriptionRenewalMode("extend")
	require.NoError(t, err)
	require.Equal(t, SubscriptionRenewalModeExtend, mode)

	_, err = NormalizeSubscriptionRenewalMode("invalid")
	require.Error(t, err)
	require.Equal(t, "INVALID_RENEWAL_MODE", infraerrorsReason(err))
}

func TestExtendSubscriptionUsesLockedCurrentRow(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	initialExpiry := now.AddDate(0, 0, 10)
	repo := &lockingRenewalRepo{current: UserSubscription{
		ID: 37, UserID: 41, GroupID: 43, ExpiresAt: initialExpiry, Status: SubscriptionStatusActive,
	}}
	svc := NewSubscriptionService(nil, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	updated, err := svc.ExtendSubscription(context.Background(), 7, 5)

	require.NoError(t, err)
	require.Equal(t, 1, repo.lockReads)
	require.Equal(t, initialExpiry.AddDate(0, 0, 5), updated.ExpiresAt)
}

func TestAssignSubscriptionDoesNotReactivateRowSuspendedAfterStaleRead(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-24 * time.Hour)
	current := UserSubscription{
		ID: 27, UserID: 31, GroupID: 33, StartsAt: now.AddDate(0, 0, -10), ExpiresAt: now.Add(-time.Hour),
		Status: SubscriptionStatusSuspended, Notes: "suspended", DailyWindowStart: &windowStart, DailyUsageUSD: 4,
	}
	repo := &lockingRenewalRepo{
		stale:   UserSubscription{ID: 27, UserID: 31, GroupID: 33, ExpiresAt: now.Add(-time.Hour), Status: SubscriptionStatusExpired},
		current: current,
	}
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: &Group{ID: 33, SubscriptionType: SubscriptionTypeSubscription}}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	sub, reused, err := svc.assignSubscriptionWithReuse(context.Background(), &AssignSubscriptionInput{
		UserID: 31, GroupID: 33, ValidityDays: 5, Notes: "renewed",
	})

	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, 1, repo.lockReads)
	require.Equal(t, current, repo.current)
	require.Equal(t, SubscriptionStatusSuspended, sub.Status)
	require.Equal(t, current.ExpiresAt, sub.ExpiresAt)
	require.Equal(t, current.Notes, sub.Notes)
}
