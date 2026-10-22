package service

// BalanceRejectionNotifier schedules an alert for a rejection based on actual
// user balance. Subscription/key quotas and inflight reservations must not call it.
type BalanceRejectionNotifier interface {
	CheckBalanceOnRejection(user *User, balance float64)
}

func (s *APIKeyService) SetBalanceRejectionNotifier(notifier BalanceRejectionNotifier) {
	s.balanceRejectionNotifier = notifier
}

func (s *APIKeyService) NotifyBalanceRejected(user *User) {
	if s.balanceRejectionNotifier != nil && user != nil {
		s.balanceRejectionNotifier.CheckBalanceOnRejection(user, user.Balance)
	}
}

func (s *BillingCacheService) SetBalanceRejectionNotifier(notifier BalanceRejectionNotifier) {
	s.balanceRejectionNotifier = notifier
}
