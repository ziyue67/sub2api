//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balanceMailStub struct {
	calls atomic.Int32
	fail  atomic.Bool
	block chan struct{}
}

func (m *balanceMailStub) SendEmail(context.Context, string, string, string) error {
	m.calls.Add(1)
	if m.block != nil {
		<-m.block
	}
	if m.fail.Load() {
		return errors.New("synthetic delivery failure")
	}
	return nil
}
func balanceRejectionFixture(t *testing.T) (*BalanceNotifyService, *notificationEmailMemorySettingRepo, *balanceMailStub, *User) {
	t.Helper()
	repo := newNotificationEmailMemorySettingRepo()
	require.NoError(t, repo.SetMultiple(context.Background(), map[string]string{SettingKeyBalanceLowNotifyEnabled: "true", SettingKeyBalanceLowNotifyThreshold: "10"}))
	mail := &balanceMailStub{}
	svc := NewBalanceNotifyService(nil, repo, nil)
	svc.emailService = mail
	user := &User{ID: 71, BalanceNotifyEnabled: true, BalanceNotifyExtraEmails: []NotifyEmailEntry{{Email: "synthetic@example.invalid", Verified: true}}}
	return svc, repo, mail, user
}
func waitBalanceIdle(t *testing.T, s *BalanceNotifyService) {
	t.Helper()
	require.Eventually(t, func() bool { s.balanceMu.Lock(); defer s.balanceMu.Unlock(); return s.balanceActive == 0 }, time.Second, time.Millisecond)
}
func expireBalanceCooldown(s *BalanceNotifyService, id int64) {
	s.balanceMu.Lock()
	defer s.balanceMu.Unlock()
	s.balanceAttempts[id] = balanceNotifyAttempt{}
}
func TestBalanceRejectionRespectsNotificationConfiguration(t *testing.T) {
	for _, name := range []string{"enabled", "user disabled", "global disabled", "unverified", "recipient disabled", "no recipients", "above custom threshold", "percentage threshold", "zero threshold", "unsubscribed"} {
		t.Run(name, func(t *testing.T) {
			svc, repo, mail, user := balanceRejectionFixture(t)
			balance := 0.0
			want := int32(0)
			switch name {
			case "enabled":
				want = 1
			case "user disabled":
				user.BalanceNotifyEnabled = false
			case "global disabled":
				require.NoError(t, repo.Set(context.Background(), SettingKeyBalanceLowNotifyEnabled, "false"))
			case "unverified":
				user.BalanceNotifyExtraEmails[0].Verified = false
			case "recipient disabled":
				user.BalanceNotifyExtraEmails[0].Disabled = true
			case "no recipients":
				user.BalanceNotifyExtraEmails = nil
			case "above custom threshold":
				v := 0.01
				user.BalanceNotifyThreshold = &v
				balance = 0.02
			case "percentage threshold":
				v := 10.0
				user.BalanceNotifyThreshold = &v
				user.BalanceNotifyThresholdType = thresholdTypePercentage
				user.TotalRecharged = 100
				balance = 9
				want = 1
			case "zero threshold":
				v := 0.0
				user.BalanceNotifyThreshold = &v
			case "unsubscribed":
				require.NoError(t, repo.Set(context.Background(), notificationEmailPreferenceKey(NotificationEmailEventBalanceLow, user.BalanceNotifyExtraEmails[0].Email), "unsubscribed"))
			}
			svc.CheckBalanceOnRejection(user, balance)
			waitBalanceIdle(t, svc)
			require.Equal(t, want, mail.calls.Load())
		})
	}
}
func TestBalanceRejectionFailureRetryAndDailySuccess(t *testing.T) {
	svc, repo, mail, user := balanceRejectionFixture(t)
	mail.fail.Store(true)
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(1), mail.calls.Load())
	key := notificationEmailDeliveryKey(NotificationEmailEventBalanceLow, "balance_low", "71", user.BalanceNotifyExtraEmails[0].Email, time.Now().UTC().Format("2006-01-02"))
	_, err := repo.GetValue(context.Background(), key)
	require.ErrorIs(t, err, ErrSettingNotFound)
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(1), mail.calls.Load(), "failure cooldown")
	expireBalanceCooldown(svc, user.ID)
	mail.fail.Store(false)
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(2), mail.calls.Load())
	_, err = repo.GetValue(context.Background(), key)
	require.NoError(t, err)
	expireBalanceCooldown(svc, user.ID)
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(2), mail.calls.Load(), "daily success dedup")
	// Post-deduction alert shares the exact delivery identity.
	expireBalanceCooldown(svc, user.ID)
	svc.CheckBalanceAfterDeduction(context.Background(), user, 11, 2)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(2), mail.calls.Load())
}
func TestBalanceRejectionNonblockingAndConcurrentDedup(t *testing.T) {
	svc, _, mail, user := balanceRejectionFixture(t)
	mail.block = make(chan struct{})
	defer func() { close(mail.block); waitBalanceIdle(t, svc) }()
	svc.CheckBalanceOnRejection(user, 0)
	require.Eventually(t, func() bool { return mail.calls.Load() == 1 }, time.Second, time.Millisecond)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			svc.CheckBalanceOnRejection(user, 0)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SMTP blocked request path")
	}
	require.Equal(t, int32(1), mail.calls.Load())
}
func TestBalanceNotificationAdmissionIsBounded(t *testing.T) {
	svc := &BalanceNotifyService{}
	now := time.Now()
	for id := int64(1); id <= balanceNotifyMaxActive; id++ {
		require.True(t, svc.beginBalanceEmail(id, now))
	}
	require.False(t, svc.beginBalanceEmail(99, now))
	require.False(t, svc.beginBalanceEmail(1, now.Add(time.Hour)), "active user cannot be retried")
}

func TestBalanceRejectionTemplateFallbackSharesSuccessMarker(t *testing.T) {
	svc, repo, mail, user := balanceRejectionFixture(t)
	svc.SetNotificationEmailService(NewNotificationEmailService(repo, NewEmailService(repo, nil)))
	// Corrupt override exercises the existing template-error fallback without SMTP.
	require.NoError(t, repo.Set(context.Background(), notificationEmailTemplateKey(NotificationEmailEventBalanceLow, "en"), "invalid-json"))
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(1), mail.calls.Load())
	expireBalanceCooldown(svc, user.ID)
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Equal(t, int32(1), mail.calls.Load())
}
func TestBalanceRejectionTemplateDeliveryFailureDoesNotFallbackOrMark(t *testing.T) {
	svc, repo, mail, user := balanceRejectionFixture(t)
	// Missing SMTP config fails before any network attempt.
	svc.SetNotificationEmailService(NewNotificationEmailService(repo, NewEmailService(repo, nil)))
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	require.Zero(t, mail.calls.Load(), "delivery failure must not trigger fallback")
	key := notificationEmailDeliveryKey(NotificationEmailEventBalanceLow, "balance_low", "71", user.BalanceNotifyExtraEmails[0].Email, time.Now().UTC().Format("2006-01-02"))
	_, err := repo.GetValue(context.Background(), key)
	require.ErrorIs(t, err, ErrSettingNotFound)
	expireBalanceCooldown(svc, user.ID)
	svc.CheckBalanceOnRejection(user, 0)
	waitBalanceIdle(t, svc)
	_, err = repo.GetValue(context.Background(), key)
	require.ErrorIs(t, err, ErrSettingNotFound)
	require.Zero(t, mail.calls.Load())
}
