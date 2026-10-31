package requesttiming

import (
	"context"
	"time"
)

type clockKey struct{}

// WithClock supplies a request-scoped observation clock. Deadlines and resource
// leases continue to use the real clock. The function must be safe for concurrent use.
func WithClock(ctx context.Context, now func() time.Time) context.Context {
	return context.WithValue(ctx, clockKey{}, now)
}

// Now samples an observation boundary, retaining time.Now's monotonic component.
func Now(ctx context.Context) time.Time {
	if ctx != nil {
		if now, ok := ctx.Value(clockKey{}).(func() time.Time); ok && now != nil {
			return now()
		}
	}
	return time.Now()
}
