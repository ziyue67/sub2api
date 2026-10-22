package service

import (
	"context"
	"log/slog"
	"time"
)

const (
	balanceNotifyRetryCooldown = time.Minute
	balanceNotifyMaxActive     = 4
	balanceNotifyMaxUsers      = 4096
)

type balanceNotifyAttempt struct {
	next   time.Time
	active bool
}

// CheckBalanceOnRejection does no settings/SMTP I/O on the request path.
// A failed attempt can retry on a subsequent rejection after the cooldown.
func (s *BalanceNotifyService) CheckBalanceOnRejection(user *User, balance float64) {
	if !s.canNotifyBalance(user) {
		return
	}
	s.scheduleBalanceEmail(user, func(ctx context.Context, snapshot *User) {
		threshold, rechargeURL, ok := s.resolveUserEffectiveThreshold(ctx, snapshot)
		if ok && balance < threshold {
			s.sendResolvedBalanceEmail(ctx, snapshot, balance, threshold, rechargeURL)
		}
	})
}

func (s *BalanceNotifyService) sendResolvedBalanceEmail(ctx context.Context, user *User, balance, threshold float64, rechargeURL string) {
	recipients := s.collectBalanceNotifyRecipients(user)
	if len(recipients) == 0 {
		return
	}
	s.sendBalanceLowEmails(recipients, user.ID, user.Username, user.Email, balance, threshold, s.getSiteName(ctx), rechargeURL)
}

func (s *BalanceNotifyService) beginBalanceEmail(userID int64, now time.Time) bool {
	s.balanceMu.Lock()
	defer s.balanceMu.Unlock()
	attempt := s.balanceAttempts[userID]
	if attempt.active || now.Before(attempt.next) || s.balanceActive >= balanceNotifyMaxActive {
		return false
	}
	if len(s.balanceAttempts) >= balanceNotifyMaxUsers {
		for id, entry := range s.balanceAttempts {
			if !entry.active && !now.Before(entry.next) {
				delete(s.balanceAttempts, id)
			}
		}
		if len(s.balanceAttempts) >= balanceNotifyMaxUsers {
			return false
		}
	}
	if s.balanceAttempts == nil {
		s.balanceAttempts = make(map[int64]balanceNotifyAttempt)
	}
	s.balanceAttempts[userID] = balanceNotifyAttempt{active: true}
	s.balanceActive++
	return true
}

func (s *BalanceNotifyService) scheduleBalanceEmail(user *User, send func(context.Context, *User)) {
	if !s.beginBalanceEmail(user.ID, time.Now()) {
		return
	}
	snapshot := *user
	snapshot.BalanceNotifyExtraEmails = append([]NotifyEmailEntry(nil), user.BalanceNotifyExtraEmails...)
	if user.BalanceNotifyThreshold != nil {
		threshold := *user.BalanceNotifyThreshold
		snapshot.BalanceNotifyThreshold = &threshold
	}
	go func() {
		defer func() {
			s.balanceMu.Lock()
			s.balanceAttempts[snapshot.ID] = balanceNotifyAttempt{next: time.Now().Add(balanceNotifyRetryCooldown)}
			s.balanceActive--
			s.balanceMu.Unlock()
			if recover() != nil {
				slog.Error("panic in balance notification", "user_id", snapshot.ID)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		defer cancel()
		send(ctx, &snapshot)
	}()
}
