package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSubscriptionBillingWindowsPreserveTermAndAdvanceAligned(t *testing.T) {
	start := time.Date(2026, 1, 1, 15, 30, 0, 0, timezone.Location())
	end := start.Add(10 * 24 * time.Hour)
	s := UserSubscription{StartsAt: start, ExpiresAt: end, DailyWindowStart: &start, WeeklyWindowStart: &start, MonthlyWindowStart: &start}
	now := start.Add(9 * 24 * time.Hour)
	got := s.BillingWindowStarts(now)
	require.Equal(t, timezone.StartOfDay(now), got[0])
	require.Equal(t, start.Add(7*24*time.Hour), got[1])
	require.Equal(t, start, got[2])
	s.ExpiresAt = start.Add(24 * time.Hour)
	got = s.BillingWindowStarts(start.Add(20 * time.Hour))
	require.Equal(t, start, got[0])
	require.Equal(t, start, got[1])
	s.DailyWindowStart = nil
	got = s.BillingWindowStarts(start.Add(20 * time.Hour))
	require.Equal(t, start, got[0])
}
