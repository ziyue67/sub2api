package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
)

// Route metadata is immutable and scoped to the forwarding context, never to a
// pooled connection or Gin key reused by another account/turn. No diagnostic
// context is allocated when both existing collectors are disabled.
func withOpenAITransportRoute(ctx context.Context, downstream, mode, reason, fallback string) context.Context {
	if requestcapture.FromContext(ctx) == nil && requesttiming.From(ctx) == nil {
		return ctx
	}
	return transportdiag.WithRoute(ctx, transportdiag.Route{
		Downstream: downstream, Mode: mode, Reason: reason, FallbackReason: fallback,
	})
}

func withOpenAITransportMode(ctx context.Context, mode string) context.Context {
	route := transportdiag.RouteFromContext(ctx)
	return withOpenAITransportRoute(ctx, route.Downstream, mode, route.Reason, route.FallbackReason)
}
