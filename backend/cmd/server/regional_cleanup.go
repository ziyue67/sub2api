package main

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Stop funds before parallel cleanup can close its control-plane dependencies.
// A join timeout keeps those dependencies alive rather than racing a worker.
func stopRegionalFunds(ctx context.Context, funds *service.RegionalMetering) error {
	if funds == nil || funds.Stop == nil {
		return nil
	}
	done := make(chan struct{})
	go func() { defer close(done); funds.Stop() }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
