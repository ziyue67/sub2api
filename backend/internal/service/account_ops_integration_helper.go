//go:build integration

package service

import (
	"context"
	"net/http"
	"time"
)

// ObserveThresholdForTesting drives the normal assessor and durable observer
// with a logical clock in isolated integration builds; production has no hook.
func (s *AccountOpsService) ObserveThresholdForTesting(ctx context.Context, a *Account, kind string, now time.Time) error {
	return s.observeThreshold(ctx, a, a.ID, kind, s.currentConfig(), now, false)
}

// Delivery hooks are integration-build-only and use caller-owned fake transports.
func (s *AccountOpsService) SetNotificationHTTPClientForTesting(client *http.Client) {
	s.robotClient = client
}
func (s *AccountOpsService) DeliverNotificationForTesting(ctx context.Context, e *AccountOpsEvent) {
	s.deliverNotification(ctx, e)
}
